package devlxd

import (
	"context"

	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

// FakeOperation implements lxdClient.DevLXDOperation for testing.
type FakeOperation struct {
	lxdClient.DevLXDOperation
}

// WaitContext reports the operation as completed.
func (f *FakeOperation) WaitContext(ctx context.Context) error {
	return nil
}

// FakeServer mocks lxdClient.DevLXDServer for testing.
// Each method calls the corresponding function field if it is set, and
// returns a default value otherwise.
type FakeServer struct {
	lxdClient.DevLXDServer

	GetVolFunc    func(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error)
	UpdateVolFunc func(pool string, volType string, name string, volume api.DevLXDStorageVolumePut, ETag string) (lxdClient.DevLXDOperation, error)
}

// GetStoragePoolVolume returns the storage volume with the given name.
func (f *FakeServer) GetStoragePoolVolume(pool string, volType string, name string) (*api.DevLXDStorageVolume, string, error) {
	if f.GetVolFunc != nil {
		return f.GetVolFunc(pool, volType, name)
	}

	return nil, "", nil
}

// UpdateStoragePoolVolume updates the storage volume with the given name.
func (f *FakeServer) UpdateStoragePoolVolume(pool string, volType string, name string, volume api.DevLXDStorageVolumePut, ETag string) (lxdClient.DevLXDOperation, error) {
	if f.UpdateVolFunc != nil {
		return f.UpdateVolFunc(pool, volType, name, volume, ETag)
	}

	return &FakeOperation{}, nil
}
