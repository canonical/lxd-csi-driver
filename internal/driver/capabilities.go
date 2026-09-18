package driver

import (
	"errors"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

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

// ValidateVolumeCapabilities validates the provided volume capabilities.
func ValidateVolumeCapabilities(volCaps ...*csi.VolumeCapability) error {
	if len(volCaps) == 0 {
		return errors.New("Request has no volume capabilities")
	}

	accessTypeBlock := false
	accessTypeMount := false

	for _, c := range volCaps {
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
		csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:
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
