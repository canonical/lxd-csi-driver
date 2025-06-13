package driver

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
