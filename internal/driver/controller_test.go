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
	getPoolFunc   func(name string) (*api.DevLXDStoragePool, string, error)
	getStateFunc  func() (*api.DevLXDGet, error)
	createVolFunc func(pool string, volume api.DevLXDStorageVolumesPost) (lxdClient.DevLXDOperation, error)
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

func (f *fakeDevLXDServer) GetStoragePool(name string) (*api.DevLXDStoragePool, string, error) {
	if f.getPoolFunc != nil {
		return f.getPoolFunc(name)
	}
	return nil, "", nil
}

func (f *fakeDevLXDServer) GetState() (*api.DevLXDGet, error) {
	if f.getStateFunc != nil {
		return f.getStateFunc()
	}
	return nil, nil
}

func (f *fakeDevLXDServer) CreateStoragePoolVolume(pool string, volume api.DevLXDStorageVolumesPost) (lxdClient.DevLXDOperation, error) {
	if f.createVolFunc != nil {
		return f.createVolFunc(pool, volume)
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

func TestCreateVolumeWithBlockFilesystem(t *testing.T) {
	tests := []struct {
		name       string
		parameters map[string]string
		expectedFs string
	}{
		{
			name: "Explicit block.filesystem",
			parameters: map[string]string{
				"storagePool":         "remote",
				"block.filesystem":    "xfs",
				"block.mount_options": "noatime",
			},
			expectedFs: "xfs",
		},
		{
			name: "Standard fallback fstype",
			parameters: map[string]string{
				"storagePool":               "remote",
				"csi.storage.k8s.io/fstype": "ext4",
			},
			expectedFs: "ext4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Initialize driver and controller server
			d := &Driver{
				name:     "lxd.csi.canonical.com",
				version:  "test",
				endpoint: "unix:///csi/csi.sock",
				nodeID:   "test-node",
			}

			var calledCreate bool
			fakeClient := &fakeDevLXDServer{
				getPoolFunc: func(name string) (*api.DevLXDStoragePool, string, error) {
					return &api.DevLXDStoragePool{
						Name:   "remote",
						Driver: "dir",
					}, "test-pool-etag", nil
				},
				getStateFunc: func() (*api.DevLXDGet, error) {
					return &api.DevLXDGet{
						DevLXDGetUntrusted: api.DevLXDGetUntrusted{
							Auth: api.AuthTrusted,
							SupportedStorageDrivers: []api.DevLXDServerStorageDriverInfo{
								{Name: "dir", Remote: false},
							},
						},
					}, nil
				},
				getVolFunc: func(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error) {
					// Return not found for volume check during creation
					return nil, "", api.NewStatusError(404, "Not Found")
				},
				createVolFunc: func(pool string, volume api.DevLXDStorageVolumesPost) (lxdClient.DevLXDOperation, error) {
					calledCreate = true
					require.Equal(t, "remote", pool)
					require.Equal(t, "custom", volume.Type)
					require.Equal(t, "block", volume.ContentType)
					require.NotContains(t, volume.Config, "block.filesystem")
					require.NotContains(t, volume.Config, "block.mount_options")
					require.Equal(t, "10737418240", volume.Config["size"]) // 10Gi
					return &fakeDevLXDOperation{}, nil
				},
			}

			d.devLXD = fakeClient
			controller := NewControllerServer(d)

			req := &csi.CreateVolumeRequest{
				Name: "pvc-test-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 10737418240, // 10Gi
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: tc.expectedFs,
							},
						},
					},
				},
				Parameters: tc.parameters,
			}

			resp, err := controller.CreateVolume(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, resp.Volume)
			require.Contains(t, resp.Volume.VolumeId, "remote/pvc-")
			require.Equal(t, int64(10737418240), resp.Volume.CapacityBytes)
			require.Equal(t, "dir", resp.Volume.VolumeContext[ParameterStorageDriver])
			require.True(t, calledCreate, "CreateStoragePoolVolume should have been called")
		})
	}
}
