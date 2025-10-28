package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/devlxd"
	"github.com/canonical/lxd-csi-driver/internal/fs"
	"github.com/canonical/lxd-csi-driver/internal/utils"
	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	lxdValidate "github.com/canonical/lxd/shared/validate"
)

// driverVersion is the version of the CSI driver.
// It is set during the build.
var driverVersion = "dev"

// driverFileSystemMountPath is the path where the CSI driver mounts
// the filesystem volumes.
const driverFileSystemMountPath = "/mnt/lxd-csi"

// Default CSI driver configuration values.
const (
	// DefaultDriverName is the default name of the CSI driver.
	DefaultDriverName = "lxd.csi.canonical.com"

	// DefaultDriverEndpoint is the default unix socket path for the CSI driver.
	DefaultDriverEndpoint = "unix:///tmp/csi.sock"

	// DefaultVolumeNamePrefix is the default prefix used for LXD volume names.
	// Volume names are in format "<prefix>-<uuid>".
	DefaultVolumeNamePrefix = "csi"

	// DefaultDevLXDEndpoint is the default unix socket path for connecting to DevLXD.
	DefaultDevLXDEndpoint = "unix:///dev/lxd/sock"

	// DefaultDevLXDTokenFile is the default path to the file containing the bearer token
	// for authenticating with devLXD.
	DefaultDevLXDTokenFile = "/etc/lxd-csi-driver/token"
)

const (
	// AnnotationLXDClusterMember is the name of the annotation that
	// specifies the location for the CSINode and volume.
	AnnotationLXDClusterMember = "lxd.csi.canonical.com/cluster-member"
)

const (
	// ParameterStoragePool is the name of the storage class parameter
	// that specifies the LXD storage pool to use.
	//
	// This is required parameter and must be set by the user.
	ParameterStoragePool = "storagePool"

	// ParameterStorageDriver is the name of the underlying storage pool
	// driver.
	//
	// This is internal parameter used only by the CSI driver.
	ParameterStorageDriver = "internal.storageDriver"

	// ParameterPVCName contains the name of the PVC that triggered volume creation.
	// It is passed to the controller by the CSI provisioner.
	ParameterPVCName = "csi.storage.k8s.io/pvc/name"

	// ParameterPVCNamespace contains the namespace of the PVC that triggered volume creation.
	// It is passed to the controller by the CSI provisioner.
	ParameterPVCNamespace = "csi.storage.k8s.io/pvc/namespace"

	// ParameterPVName contains the name of the PV that represents the LXD volume.
	// It is passed to the controller by the CSI provisioner.
	ParameterPVName = "csi.storage.k8s.io/pv/name"
)

// DriverOptions contains the configurable options for the driver.
type DriverOptions struct {
	// Name of the driver.
	Name string

	// CSI endpoint (unix).
	Endpoint string

	// DevLXD endpoint (unix).
	DevLXDEndpoint string

	// Prefix used for LXD volume names.
	VolumeNamePrefix string

	// ID of the node where the driver is running.
	NodeID string

	// IsController indicates whether to start controller server.
	IsController bool
}

// Driver represents a CSI driver for LXD.
type Driver struct {
	// General driver information.
	name         string
	version      string
	endpoint     string
	nodeID       string
	isController bool

	// Capabilities.
	controllerCapabilities []*csi.ControllerServiceCapability
	nodeCapabilities       []*csi.NodeServiceCapability

	// DevLXD.
	devLXD         lxdClient.DevLXDServer
	devLXDEndpoint string

	// Path to the file containing the bearer token for authenticating with devLXD.
	devLXDTokenFile string

	// Whether file containing devLXD bearer token needs to be re-read.
	hasDevLXDTokenChanged bool

	// LXD cluster member where instance is running on.
	location    string
	isClustered bool

	// Prefix used for LXD volume names.
	volumeNamePrefix string

	// gRPC server.
	server *grpc.Server

	// Lock for accessing/modifying driver.
	lock sync.Mutex
}

// NewDriver initializes a new CSI driver.
func NewDriver(opts DriverOptions) *Driver {
	d := &Driver{
		name:             opts.Name,
		version:          driverVersion,
		endpoint:         opts.Endpoint,
		devLXDEndpoint:   opts.DevLXDEndpoint,
		devLXDTokenFile:  DefaultDevLXDTokenFile,
		volumeNamePrefix: opts.VolumeNamePrefix,
		nodeID:           opts.NodeID,
		isController:     opts.IsController,
	}

	return d
}

// Version returns the driver version.
func (d *Driver) Version() string {
	return d.version
}

// Validate checks whether the driver configuration is valid.
func (d *Driver) Validate() error {
	// Validate volume name prefix.
	// Ensure the volume name prefix is not longer than 63 characters. The full name is
	// generated as "<prefix>-<uuid>", where the UUID is 36 characters plus hyphen.
	// Although the maximum volume name length varies by LXD storage driver, we cap the name
	// length at 100 characters to stay within safe limits.
	err := lxdValidate.IsHostname(d.volumeNamePrefix)
	if err != nil {
		return fmt.Errorf("Volume name prefix %q is not valid: %w", d.volumeNamePrefix, err)
	}

	return nil
}

// DevLXDClient returns the connected DevLXD client.
// If devLXD token has changed, or connection has not been established yet, a new client is returned.
func (d *Driver) DevLXDClient() (lxdClient.DevLXDServer, error) {
	// Return connected client if it exists.
	d.lock.Lock()
	defer d.lock.Unlock()

	// Return existing client if it exists and the token has not changed.
	if d.devLXD != nil && !d.hasDevLXDTokenChanged {
		return d.devLXD, nil
	}

	var devLXDClient lxdClient.DevLXDServer

	// Read token from the mounted file.
	tokenBytes, err := os.ReadFile(d.devLXDTokenFile)
	if err != nil {
		return nil, fmt.Errorf("Failed reading DevLXD bearer token from file %q: %w", d.devLXDTokenFile, err)
	}

	token := string(tokenBytes)

	// If the client is initialized, but the token has changed, update it.
	if d.devLXD != nil && d.hasDevLXDTokenChanged {
		// Update client with new token.
		devLXDClient = d.devLXD.UseBearerToken(token)
	} else {
		// Connect to DevLXD because DevLXD client is not initialized yet.
		devLXDClient, err = devlxd.Connect(d.devLXDEndpoint, token)
		if err != nil {
			return nil, fmt.Errorf("Failed to connect to devLXD: %w", err)
		}
	}

	// Refresh DevLXD server information.
	info, err := devLXDClient.GetState()
	if err != nil {
		return nil, fmt.Errorf("Failed to get LXD server info: %w", err)
	}

	// Fail early if not authenticated.
	// In addition, this ensures we retrieve actual information whether LXD is clustered or not.
	// If we are not authenticated, the Environment.ServerClustered field is always false.
	if info.Auth != api.AuthTrusted {
		return nil, errors.New("Failed to authenticate with DevLXD server: Client is not trusted")
	}

	d.devLXD = devLXDClient
	d.location = info.Location
	d.isClustered = info.Environment.ServerClustered
	d.hasDevLXDTokenChanged = false

	return d.devLXD, nil
}

// Run starts CSI driver gRPC server.
func (d *Driver) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	klog.InfoS("Starting LXD CSI driver",
		"name", d.name,
		"node", d.nodeID,
		"version", d.version,
	)

	// Validate drivers configuration.
	err := d.Validate()
	if err != nil {
		return err
	}

	// Connect to devLXD.
	_, err = d.DevLXDClient()
	if err != nil {
		return err
	}

	// Watch for token file changes.
	handleTokenFileChange := func(path string) {
		klog.InfoS("DevLXD token file has changed, will re-read it on next operation", "path", path)
		d.lock.Lock()
		d.hasDevLXDTokenChanged = true
		d.lock.Unlock()
	}

	err = fs.WatchFile(ctx, d.devLXDTokenFile, handleTokenFileChange)
	if err != nil {
		return fmt.Errorf("Failed to watch DevLXD token file %q for changes: %w", d.devLXDTokenFile, err)
	}

	// Construct gRPC unix address.
	url, socket, err := utils.ParseUnixSocketURL(d.endpoint)
	if err != nil {
		return err
	}

	// Delete old CSI unix socket if it exists.
	_ = os.Remove(socket)

	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("Failed to listen on %q: %w", url.String(), err)
	}

	defer func() { _ = listener.Close() }()

	d.server = grpc.NewServer()

	// Register CSI services.
	csi.RegisterIdentityServer(d.server, NewIdentityServer(d))

	if d.isController {
		d.SetControllerServiceCapabilities(
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		)

		csi.RegisterControllerServer(d.server, NewControllerServer(d))
	} else {
		d.SetNodeServiceCapabilities()
		csi.RegisterNodeServer(d.server, NewNodeServer(d))
	}

	// Start gRPC server.
	klog.InfoS("Listening for connections", "endpoint", url.String())
	err = d.server.Serve(listener)
	if err != nil {
		return fmt.Errorf("Failed to serve gRPC server: %w", err)
	}

	return nil
}

// SetControllerServiceCapabilities sets the controller service capabilities.
func (d *Driver) SetControllerServiceCapabilities(caps ...csi.ControllerServiceCapability_RPC_Type) {
	capabilities := make([]*csi.ControllerServiceCapability, len(caps))
	for i, cap := range caps {
		klog.InfoS("Enabling controller service capability", "capability", cap.String())
		capabilities[i] = NewControllerServiceCapability(cap)
	}

	d.controllerCapabilities = capabilities
}

// SetNodeServiceCapabilities sets the node service capabilities.
func (d *Driver) SetNodeServiceCapabilities(caps ...csi.NodeServiceCapability_RPC_Type) {
	capabilities := make([]*csi.NodeServiceCapability, len(caps))
	for i, cap := range caps {
		klog.InfoS("Enabling node service capability", "capability", cap.String())
		capabilities[i] = NewNodeServiceCapability(cap)
	}

	d.nodeCapabilities = capabilities
}

// getVolumeID constructs a unique volume ID based on the cluster member,
// storage pool name, and volume name.
// Returned value is in format "[<clusterMember>:]<poolName>/<volumeName>".
func getVolumeID(clusterMember string, poolName string, volName string) string {
	volumeID := poolName + "/" + volName

	if clusterMember != "" {
		volumeID = clusterMember + ":" + volumeID
	}

	return volumeID
}

// splitVolumeID splits an internal volume ID separated into cluster member name,
// pool name, and volume name.
func splitVolumeID(volumeID string) (clusterMember string, poolName string, volName string, err error) {
	if strings.Contains(volumeID, ":") {
		clusterMember, volumeID, _ = strings.Cut(volumeID, ":")
	}

	if volumeID == "" {
		return "", "", "", errors.New("Volume ID is empty")
	}

	parts := strings.Split(volumeID, "/")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("Invalid volume ID %q", volumeID)
	}

	return clusterMember, parts[0], parts[1], nil
}
