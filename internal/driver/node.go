package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
)

type nodeServer struct {
	driver *Driver

	// Must be embeded for forward compatibility.
	csi.UnimplementedNodeServer
}

// NewNodeServer returns a new instance of the CSI node server.
func NewNodeServer(driver *Driver) *nodeServer {
	return &nodeServer{
		driver: driver,
	}
}
