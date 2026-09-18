package driver

import (
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"
)

// newVolumeCapability returns a VolumeCapability with the given access mode and
// either block or mount access type.
func newVolumeCapability(mode csi.VolumeCapability_AccessMode_Mode, block bool) *csi.VolumeCapability {
	volCap := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: mode,
		},
	}

	if block {
		volCap.AccessType = &csi.VolumeCapability_Block{
			Block: &csi.VolumeCapability_BlockVolume{},
		}
	} else {
		volCap.AccessType = &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		}
	}

	return volCap
}

func TestValidateVolumeCapabilities(t *testing.T) {
	tests := []struct {
		Name               string
		VolumeCapabilities []*csi.VolumeCapability
		expectError        string
	}{
		{
			Name: "Ensure single node writer is accepted",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, false),
			},
			expectError: "",
		},
		{
			Name: "Ensure single node single writer is accepted",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER, false),
			},
			expectError: "",
		},
		{
			Name: "Ensure single node multi writer is accepted",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER, false),
			},
			expectError: "",
		},
		{
			Name: "Ensure single node reader only is accepted",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY, false),
			},
			expectError: "",
		},
		{
			Name: "Ensure single node writer is accepted for block volume",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, true),
			},
			expectError: "",
		},
		{
			Name: "Ensure unset access mode is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
				},
			},
			expectError: `Access mode "UNKNOWN" is not supported`,
		},
		{
			Name: "Ensure unknown access mode is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_UNKNOWN, false),
			},
			expectError: `Access mode "UNKNOWN" is not supported`,
		},
		{
			Name: "Ensure undefined access mode is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_Mode(99), false),
			},
			expectError: `Access mode "99" is not supported`,
		},
		{
			Name: "Ensure multi node multi writer is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER, false),
			},
			expectError: `Access mode "MULTI_NODE_MULTI_WRITER" is not supported`,
		},
		{
			Name: "Ensure multi node single writer is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER, false),
			},
			expectError: `Access mode "MULTI_NODE_SINGLE_WRITER" is not supported`,
		},
		{
			Name: "Ensure multi node reader only is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY, false),
			},
			expectError: `Access mode "MULTI_NODE_READER_ONLY" is not supported`,
		},
		{
			Name: "Ensure any multi node capability is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, false),
				newVolumeCapability(csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER, false),
			},
			expectError: `Access mode "MULTI_NODE_MULTI_WRITER" is not supported`,
		},
		{
			Name: "Ensure nil capability is rejected",
			VolumeCapabilities: []*csi.VolumeCapability{
				newVolumeCapability(csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER, false),
				nil,
			},
			expectError: "VolumeCapability cannot be nil",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			err := ValidateVolumeCapabilities(test.VolumeCapabilities...)
			if test.expectError == "" {
				require.NoError(t, err, "Expected no error, got %v", err)
			} else {
				if err == nil {
					require.FailNowf(t, "Expected error %q, got none", test.expectError)
				}

				require.ErrorContains(t, err, test.expectError)
			}
		})
	}
}
