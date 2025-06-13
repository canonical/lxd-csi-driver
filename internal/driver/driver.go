package driver

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	"github.com/canonical/lxd-csi-driver/internal/devlxd"
	"github.com/canonical/lxd-csi-driver/internal/utils"
	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

// driverVersion is the version of the CSI driver.
// It is set during the build.
const driverVersion = "dev"

// Default CSI driver configuration values.
const (
	DefaultDriverName     = "lxd.csi.canonical.com"
	DefaultDriverEndpoint = "unix:///tmp/csi.sock"
	DefaultDevLXDEndpoint = "unix:///dev/lxd/sock"
)

// DriverOptions contains the configurable options for the driver.
type DriverOptions struct {
	// Name of the driver.
	Name string

	// CSI endpoint (unix).
	Endpoint string

	// DevLXD endpoint (unix).
	DevLXDEndpoint string

	// ID of the node where the driver is running.
	NodeID string
}

// Driver represents a CSI driver for LXD.
type Driver struct {
	// General driver information.
	name     string
	version  string
	endpoint string
	nodeID   string

	// Capabilities.
	controllerCapabilities []*csi.ControllerServiceCapability
	nodeCapabilities       []*csi.NodeServiceCapability

	// DevLXD.
	devLXD         lxdClient.DevLXDServer
	devLXDEndpoint string

	// LXD cluster member where instance is running on.
	location    string
	isClustered bool

	// gRPC server.
	server *grpc.Server
}

// NewDriver initializes a new CSI driver.
func NewDriver(opts DriverOptions) *Driver {
	d := &Driver{
		name:           opts.Name,
		version:        driverVersion,
		endpoint:       opts.Endpoint,
		devLXDEndpoint: opts.DevLXDEndpoint,
		nodeID:         opts.NodeID,
	}

	return d
}

// Run starts CSI driver gRPC server.
func (d *Driver) Run() error {
	klog.InfoS("Starting LXD CSI driver",
		"name", d.name,
		"node", d.nodeID,
		"version", d.version,
	)

	// Connect to devLXD.
	devLXDClient, err := devlxd.Connect(d.devLXDEndpoint)
	if err != nil {
		return fmt.Errorf("Failed to connect to devLXD: %v", err)
	}

	info, err := devLXDClient.GetState()
	if err != nil {
		return fmt.Errorf("Failed to get LXD server info: %v", err)
	}

	// Fail early if not authenticated.
	// In addition, this ensures we retrieve actual information whether LXD is clustered or not.
	// If we are not authenticated, the Environment.ServerClustered field is always false.
	if info.Auth != api.AuthTrusted {
		return errors.New("Failed to authenticate with DevLXD server: Client is not tursted")
	}

	d.devLXD = devLXDClient
	d.location = info.Location
	d.isClustered = info.Environment.ServerClustered

	// Construct gRPC unix address.
	url, socket, err := utils.ParseUnixSocketURL(d.endpoint)
	if err != nil {
		return err
	}

	// Delete old CSI unix socket if it exists.
	_ = os.Remove(socket)

	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("Failed to listen on %q: %v", url.String(), err)
	}

	defer func() { _ = listener.Close() }()

	d.server = grpc.NewServer()

	// Register CSI services.
	csi.RegisterIdentityServer(d.server, NewIdentityServer(d))
	csi.RegisterControllerServer(d.server, NewControllerServer(d))
	csi.RegisterNodeServer(d.server, NewNodeServer(d))

	// Start gRPC server.
	klog.Infof("Listening for connections on address %q", url.String())
	err = d.server.Serve(listener)
	if err != nil {
		return fmt.Errorf("Failed to serve gRPC server: %v", err)
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

// VolumeDescription returns the generic description for the volume
// that is managed by the CSI driver.
func (d *Driver) VolumeDescription() string {
	return "Managed by " + d.name
}
