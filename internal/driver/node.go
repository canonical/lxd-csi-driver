package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/canonical/lxd-csi-driver/internal/fs"
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

// NodePublishVolume mounts a filesystem volume or maps a block volume into the pod’s
// target path on this node.
func (n *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	err := ValidateVolumeCapabilities(req.VolumeCapability)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "NodePublishVolume: %v", err)
	}

	_, _, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "NodePublishVolume: %v", err)
	}

	targetPath := req.TargetPath
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume: Target path not provided")
	}

	contentType := ParseContentType(req.VolumeCapability)
	if contentType == "" {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume: Volume capability must specify either block or filesystem access type")
	}

	// Mount options for the bind mount.
	// If the volume is read-only, add "ro" option as well.
	mountOptions := []string{"bind"}
	if req.Readonly {
		mountOptions = append(mountOptions, "ro")
	}

	mounted, err := fs.IsMounted(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("NodePublishVolume: %v", err))
	}

	if mounted {
		// Already mounted, nothing to do.
		return &csi.NodePublishVolumeResponse{}, nil
	}

	var sourcePath string

	switch req.VolumeCapability.AccessType.(type) {
	case *csi.VolumeCapability_Block:
		// Get the disk device path for the block volume.
		sourcePath, err = getDiskDevicePath(volName)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "NodePublishVolume: Source device for volume %q not found: %v", volName, err)
		}
	case *csi.VolumeCapability_Mount:
		// Construct the source path for the filesystem volume.
		sourcePath = filepath.Join(driverFileSystemMountPath, volName)

		// Read mount flags from the request.
		mnt := req.VolumeCapability.GetMount()
		mountOptions = append(mountOptions, mnt.MountFlags...)

		// Ensure source path is available.
		if !fs.PathExists(sourcePath) {
			return nil, status.Errorf(codes.NotFound, "NodePublishVolume: Source path %q not found", sourcePath)
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "NodePublishVolume: Unsupported access type %q", req.VolumeCapability.AccessType)
	}

	// Bind mount the volume to the target path (application container).
	err = fs.Mount(sourcePath, targetPath, contentType, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "NodePublishVolume: %v", err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmounts a filesystem volume or unmaps a block volume from the
// pod’s target path on this node.
func (n *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.TargetPath
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeUnpublishVolume: Target path not provided")
	}

	err := fs.Unmount(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "NodeUnpublishVolume: %v", err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
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
