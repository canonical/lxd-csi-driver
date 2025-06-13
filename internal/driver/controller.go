package driver

import (
	"context"

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

// ControllerGetCapabilities returns the capabilities of the controller server.
func (c *controllerServer) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: c.driver.controllerCapabilities,
	}, nil
}
