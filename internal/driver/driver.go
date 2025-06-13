package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/klog/v2"
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

type Driver struct {
	// General driver information.
	name     string
	version  string
	endpoint string
	nodeID   string

	// Capabilities.
	controllerCapabilities []*csi.ControllerServiceCapability
	nodeCapabilities       []*csi.NodeServiceCapability
	pluginCapabilities     []*csi.PluginCapability

	// DevLXD.
	devLXDEndpoint string
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

func (d *Driver) Run() error {
	return nil
}

// SetControllerServiceCapabilities sets the controller service capabilities.
func (d *Driver) SetControllerServiceCapabilities(caps ...csi.ControllerServiceCapability_RPC_Type) {
	capabilities := make([]*csi.ControllerServiceCapability, len(caps))
	for _, cap := range caps {
		klog.InfoS("Enabling controller service capability", "capability", cap.String())
		capabilities = append(capabilities, NewControllerServiceCapability(cap))
	}

	d.controllerCapabilities = capabilities
}

// SetNodeServiceCapabilities sets the node service capabilities.
func (d *Driver) SetNodeServiceCapabilities(caps ...csi.NodeServiceCapability_RPC_Type) {
	capabilities := make([]*csi.NodeServiceCapability, len(caps))
	for _, cap := range caps {
		klog.InfoS("Enabling node service capability", "capability", cap.String())
		capabilities = append(capabilities, NewNodeServiceCapability(cap))
	}

	d.nodeCapabilities = capabilities
}
