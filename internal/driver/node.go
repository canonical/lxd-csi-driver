package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// getDiskDevicePath returns the disk device path for a given volume name.
func getDiskDevicePath(volName string) (string, error) {
	// LXD uses a prefix of a device name and "-" is replaced with "--".
	// To match the device, we first extract the disk name from the device name by
	// separating the name on "_lxd_" and then ensure the resulting substring is a
	// prefix of the actual volume name.
	basePath := "/dev/disk/by-id"
	devices, err := os.ReadDir(basePath)
	if err != nil {
		return "", fmt.Errorf("Failed to list disk devices: %v", err)
	}

	// Replace "-" with "--" in the volume name to match the device name format.
	volDevName := strings.ReplaceAll(volName, "-", "--")

	for _, device := range devices {
		// Example device name: "scsi-0QEMU_QEMU_HARDDISK_lxd_pvc--8722b28c--a".
		// We are interested only in the device name suffix "pvc--8722b28c--a" after "_lxd_".
		_, suffix, ok := strings.Cut(device.Name(), "_lxd_")
		if !ok {
			continue
		}

		// Device name suffix should be a prefix of a volume name.
		if strings.HasPrefix(volDevName, suffix) {
			devPath := filepath.Join(basePath, device.Name())
			return filepath.EvalSymlinks(devPath)
		}
	}

	return "", fmt.Errorf("Disk device not found for volume %q", volName)
}
