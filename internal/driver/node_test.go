package driver

import (
	"context"
	"path/filepath"
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

func TestNodeGetVolumeStatsInvalidRequest(t *testing.T) {
	node := NewNodeServer(&Driver{})
	dir := t.TempDir()

	tests := []struct {
		name         string
		req          *csi.NodeGetVolumeStatsRequest
		expectedCode codes.Code
	}{
		{
			name:         "Empty volume ID",
			req:          &csi.NodeGetVolumeStatsRequest{VolumePath: dir},
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "Malformed volume ID",
			req:          &csi.NodeGetVolumeStatsRequest{VolumeId: "pvc-volume-name", VolumePath: dir},
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "Empty volume path",
			req:          &csi.NodeGetVolumeStatsRequest{VolumeId: "remote/pvc-volume-name"},
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "Non-existing volume path",
			req:          &csi.NodeGetVolumeStatsRequest{VolumeId: "remote/pvc-volume-name", VolumePath: filepath.Join(dir, "non-existing")},
			expectedCode: codes.NotFound,
		},
		{
			name:         "Volume path is not a mount point",
			req:          &csi.NodeGetVolumeStatsRequest{VolumeId: "remote/pvc-volume-name", VolumePath: dir},
			expectedCode: codes.NotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp, err := node.NodeGetVolumeStats(t.Context(), test.req)
			require.Nil(t, resp)
			require.Equal(t, test.expectedCode, status.Code(err))
		})
	}
}

// Filesystem volumes report both the capacity and the inode usage.
func TestNodeGetVolumeStatsFilesystem(t *testing.T) {
	node := NewNodeServer(&Driver{})

	// The driver reports statistics only for an actively mounted volume path.
	// The root filesystem is used, as it is guaranteed to be a mount point.
	volStatsReq := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   "remote/pvc-volume-name",
		VolumePath: "/",
	}

	resp, err := node.NodeGetVolumeStats(t.Context(), volStatsReq)
	require.NoError(t, err)
	require.Len(t, resp.Usage, 2)

	// Exact values depend on the filesystem the test is running on, therefore
	// only the relation between the reported values is checked.
	capacity := resp.Usage[0]
	require.Equal(t, csi.VolumeUsage_BYTES, capacity.Unit)
	require.Positive(t, capacity.Total)
	require.LessOrEqual(t, capacity.Used+capacity.Available, capacity.Total)

	inodes := resp.Usage[1]
	require.Equal(t, csi.VolumeUsage_INODES, inodes.Unit)
	require.Equal(t, inodes.Total, inodes.Used+inodes.Available)
}
