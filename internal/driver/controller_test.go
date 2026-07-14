package driver

import (
	"context"
	"maps"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"

	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

// fakeDevLXDOperation implements lxdClient.DevLXDOperation for testing.
type fakeDevLXDOperation struct {
	lxdClient.DevLXDOperation
}

func (f *fakeDevLXDOperation) WaitContext(ctx context.Context) error {
	return nil
}

// fakeDevLXDServer mocks lxdClient.DevLXDServer for testing.
type fakeDevLXDServer struct {
	lxdClient.DevLXDServer

	getVolFunc    func(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error)
	updateVolFunc func(pool string, volType string, name string, volume api.DevLXDStorageVolumePut, ETag string) (lxdClient.DevLXDOperation, error)
}

func (f *fakeDevLXDServer) GetStoragePoolVolume(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error) {
	if f.getVolFunc != nil {
		return f.getVolFunc(pool, volType, name)
	}
	return nil, "", nil
}

func (f *fakeDevLXDServer) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.DevLXDStorageVolumePut, ETag string) (lxdClient.DevLXDOperation, error) {
	if f.updateVolFunc != nil {
		return f.updateVolFunc(pool, volType, name, volume, ETag)
	}
	return &fakeDevLXDOperation{}, nil
}

func TestControllerExpandVolumePreservesConfig(t *testing.T) {
	// Initialize driver and controller server
	d := &Driver{
		name:     "lxd.csi.canonical.com",
		version:  "test",
		endpoint: "unix:///csi/csi.sock",
		nodeID:   "test-node",
	}

	// Create our fake LXD client
	var calledGet, calledUpdate bool
	initialConfig := map[string]string{
		"size":             "21474836480", // 20Gi
		"block.filesystem": "ext4",
		"other.custom.key": "some-value",
	}

	fakeClient := &fakeDevLXDServer{
		getVolFunc: func(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error) {
			calledGet = true
			require.Equal(t, "remote", pool)
			require.Equal(t, "custom", volType)
			require.Equal(t, "pvc-volume-name", name)
			return &api.DevLXDStorageVolume{
				Name:        "pvc-volume-name",
				Type:        "custom",
				Description: "Initial description",
				Config:      maps.Clone(initialConfig),
			}, "test-etag", nil
		},
		updateVolFunc: func(pool string, volType string, name string, volume api.DevLXDStorageVolumePut, ETag string) (lxdClient.DevLXDOperation, error) {
			calledUpdate = true
			require.Equal(t, "remote", pool)
			require.Equal(t, "custom", volType)
			require.Equal(t, "pvc-volume-name", name)
			require.Equal(t, "test-etag", ETag)
			require.Equal(t, "Initial description", volume.Description)

			// Assert that size is updated and block.filesystem and other keys are preserved
			require.Equal(t, "32212254720", volume.Config["size"]) // 30Gi
			require.Equal(t, "ext4", volume.Config["block.filesystem"])
			require.Equal(t, "some-value", volume.Config["other.custom.key"])
			return &fakeDevLXDOperation{}, nil
		},
	}

	// Inject the fake client directly into the driver
	d.devLXD = fakeClient

	controller := NewControllerServer(d)

	// Invoke ControllerExpandVolume
	req := &csi.ControllerExpandVolumeRequest{
		VolumeId: "remote/pvc-volume-name",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 32212254720, // 30Gi
		},
		VolumeCapability: &csi.VolumeCapability{
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{
					FsType: "ext4",
				},
			},
		},
	}

	resp, err := controller.ControllerExpandVolume(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, int64(32212254720), resp.CapacityBytes)

	require.True(t, calledGet, "GetStoragePoolVolume should have been called")
	require.True(t, calledUpdate, "UpdateStoragePoolVolume should have been called")
}
