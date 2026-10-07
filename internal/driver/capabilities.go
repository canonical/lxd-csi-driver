package driver

import (
	"errors"
	"fmt"
	"slices"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// multiNodeStorageDrivers lists the LXD storage drivers that support mounting
// a volume on multiple cluster members at once.
var multiNodeStorageDrivers = []string{"cephfs"}

// IsMultiNodeStorageDriver reports whether the given LXD storage driver supports
// multi-node access modes.
func IsMultiNodeStorageDriver(storageDriver string) bool {
	return slices.Contains(multiNodeStorageDrivers, storageDriver)
}

// NewControllerServiceCapability creates a new ControllerServiceCapability.
func NewControllerServiceCapability(c csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
	return &csi.ControllerServiceCapability{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: c,
			},
		},
	}
}

// NewNodeServiceCapability creates a new NodeServiceCapability.
func NewNodeServiceCapability(c csi.NodeServiceCapability_RPC_Type) *csi.NodeServiceCapability {
	return &csi.NodeServiceCapability{
		Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{
				Type: c,
			},
		},
	}
}

// ValidateVolumeCapabilities validates the provided volume capabilities against the given
// LXD storage driver. It accepts multi-node access modes only for filesystem volumes on
// a multi-node storage driver.
func ValidateVolumeCapabilities(storageDriver string, volCaps ...*csi.VolumeCapability) error {
	if len(volCaps) == 0 {
		return errors.New("Request has no volume capabilities")
	}

	accessTypeBlock := false
	accessTypeMount := false

	for _, c := range volCaps {
		if c == nil {
			return errors.New("VolumeCapability cannot be nil")
		}

		if !isSupportedAccessMode(c) {
			return fmt.Errorf("Access mode %q is not supported", c.GetAccessMode().GetMode())
		}

		if isMultiNodeAccessMode(c) {
			mode := c.GetAccessMode().GetMode()

			if c.GetBlock() != nil {
				return fmt.Errorf("Access mode %q is not supported for block volumes", mode)
			}

			if !IsMultiNodeStorageDriver(storageDriver) {
				return fmt.Errorf("Access mode %q is not supported by storage driver %q", mode, storageDriver)
			}
		}

		if c.GetBlock() != nil {
			accessTypeBlock = true
		}

		if c.GetMount() != nil {
			accessTypeMount = true
		}
	}

	if !accessTypeBlock && !accessTypeMount {
		return errors.New("VolumeCapability cannot have both the mount and the block access types undefined")
	}

	if accessTypeBlock && accessTypeMount {
		return errors.New("VolumeCapability cannot have both the mount and the block access types defined")
	}

	return nil
}

// isSupportedAccessMode reports whether the driver supports the access mode of the given
// VolumeCapability. An unset access mode is not supported.
func isSupportedAccessMode(volCap *csi.VolumeCapability) bool {
	switch volCap.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:
		return true
	default:
		return false
	}
}

// isMultiNodeAccessMode reports whether the access mode of the given VolumeCapability
// allows the volume to be attached to multiple nodes at once.
func isMultiNodeAccessMode(volCap *csi.VolumeCapability) bool {
	switch volCap.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:
		return true
	default:
		return false
	}
}

// isReadOnlyAccessMode reports whether the access mode of the given VolumeCapability
// permits only reads.
func isReadOnlyAccessMode(volCap *csi.VolumeCapability) bool {
	switch volCap.GetAccessMode().GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return true
	default:
		return false
	}
}

// ParseContentType parses the content type from the given VolumeCapability array.
func ParseContentType(volCaps ...*csi.VolumeCapability) string {
	for _, c := range volCaps {
		if c.GetBlock() != nil {
			return "block"
		}

		if c.GetMount() != nil {
			return "filesystem"
		}
	}

	return ""
}
