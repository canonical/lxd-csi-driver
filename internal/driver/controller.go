package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
)

type controllerServer struct {
	driver *Driver

	// Must be embedded for forward compatibility.
	csi.UnimplementedControllerServer
}

// NewControllerServer returns a new instance of the CSI controller server.
func NewControllerServer(driver *Driver) *controllerServer {
	return &controllerServer{
		driver: driver,
	}
}
