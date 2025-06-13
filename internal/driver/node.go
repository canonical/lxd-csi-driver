package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

type nodeServer struct {
	driver *Driver

	// Must be embedded for forward compatibility.
	csi.UnimplementedNodeServer
}

// NewNodeServer returns a new instance of the CSI node server.
func NewNodeServer(driver *Driver) *nodeServer {
	return &nodeServer{
		driver: driver,
	}
}

// NodeGetCapabilities returns the capabilities of the node server.
func (n *nodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: n.driver.nodeCapabilities,
	}, nil
}

// NodeGetInfo returns the information about the node on which the plugin is running.
func (n *nodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: n.driver.nodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				AnnotationLXDClusterMember: n.driver.location,
			},
		},
	}, nil
}
