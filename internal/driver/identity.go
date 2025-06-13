package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
)

type identityServer struct {
	driver *Driver

	// Must be embeded for forward compatibility.
	csi.UnimplementedIdentityServer
}

// NewIdentityServer returns a new instance of the CSI identity server.
func NewIdentityServer(driver *Driver) *identityServer {
	return &identityServer{
		driver: driver,
	}
}
