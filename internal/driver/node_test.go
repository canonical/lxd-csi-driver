package driver

import (
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
