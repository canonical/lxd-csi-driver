package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodePublishVolumeRejectsUnsupportedAccessMode(t *testing.T) {
	d := &Driver{
		name:     "lxd.csi.canonical.com",
		version:  "test",
		endpoint: "unix:///csi/csi.sock",
		nodeID:   "test-node",
	}

	node := NewNodeServer(d)

	req := &csi.NodePublishVolumeRequest{
		VolumeId:         "local/pvc-volume-name",
		TargetPath:       "/var/lib/kubelet/pods/test/volumes/target",
		VolumeCapability: newVolumeCapability(csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER, false),
	}

	resp, err := node.NodePublishVolume(context.Background(), req)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, `Access mode "MULTI_NODE_MULTI_WRITER" is not supported`)
}

func TestNodePublishVolumeExistingMount(t *testing.T) {
	// The test cannot create a mount without root privileges, so it uses "/proc"
	// as a target path that is already mounted read-write.
	tests := []struct {
		Name       string
		AccessMode csi.VolumeCapability_AccessMode_Mode
		Readonly   bool
		MountFlags []string
		expectCode codes.Code
	}{
		{
			Name:       "Ensure read-write request matches existing read-write mount",
			AccessMode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			expectCode: codes.OK,
		},
		{
			Name:       "Ensure read-only request is rejected for existing read-write mount",
			AccessMode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			Readonly:   true,
			expectCode: codes.AlreadyExists,
		},
		{
			Name:       "Ensure single node reader only request is rejected for existing read-write mount",
			AccessMode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
			expectCode: codes.AlreadyExists,
		},
		{
			Name:       "Ensure request with ro mount flag is rejected for existing read-write mount",
			AccessMode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			MountFlags: []string{"ro"},
			expectCode: codes.AlreadyExists,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			d := &Driver{
				name:     "lxd.csi.canonical.com",
				version:  "test",
				endpoint: "unix:///csi/csi.sock",
				nodeID:   "test-node",
			}

			node := NewNodeServer(d)

			volCap := newVolumeCapability(test.AccessMode, false)
			volCap.GetMount().MountFlags = test.MountFlags

			req := &csi.NodePublishVolumeRequest{
				VolumeId:         "local/pvc-volume-name",
				TargetPath:       "/proc",
				VolumeCapability: volCap,
				Readonly:         test.Readonly,
			}

			resp, err := node.NodePublishVolume(context.Background(), req)
			require.Equal(t, test.expectCode, status.Code(err))

			if test.expectCode == codes.OK {
				require.NotNil(t, resp)
			} else {
				require.Nil(t, resp)
			}
		})
	}
}
