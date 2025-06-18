package driver

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/shared/api"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type controllerServer struct {
	driver *Driver

	// Must be embeded for forward compatibility.
	csi.UnimplementedControllerServer
}

// NewControllerServer returns a new instance of the CSI controller server.
func NewControllerServer(driver *Driver) *controllerServer {
	return &controllerServer{
		driver: driver,
	}
}

func (c *controllerServer) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: c.driver.controllerCapabilities,
	}, nil
}

func (c *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	client := c.driver.devLXD

	volName := req.Name
	contentSource := req.VolumeContentSource

	if volName == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume: Volume name is required")
	}

	err := ValidateVolumeCapabilities(req.VolumeCapabilities...)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: %v", err)
	}

	contentType := ParseContentType(req.VolumeCapabilities...)
	if contentType == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume: Volume capability must specify either block or filesystem access type")
	}

	// Validate volume size.
	sizeBytes := req.CapacityRange.RequiredBytes
	if sizeBytes < 1 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume: Volume size cannot be zero or negative")
	}

	// Validate storage class parameters.
	parameters := req.GetParameters()
	if parameters == nil {
		parameters = make(map[string]string)
	}

	for k, v := range parameters {
		switch k {
		case ParameterStoragePool:
			parameters[k] = v
		default:
			return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Invalid parameter %q in storage class", k)
		}
	}

	poolName := req.Parameters[ParameterStoragePool]
	if poolName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Storage class parameter %q is required and cannot be empty", ParameterStoragePool)
	}

	volumeID := path.Join(poolName, volName)

	unlock, err := locking.Lock(ctx, volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to obtain lock %q: %v", volumeID, err)
	}

	defer unlock()

	pool, _, err := client.GetStoragePool(poolName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to retrieve storage pool %q: %v", poolName, err)
	}

	// Fetch the information about storage pool driver and ensure
	// it is supported.
	state, err := client.GetState()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: %v", err)
	}

	var driver *api.DevLXDServerStorageDriverInfo
	for _, d := range state.SupportedStorageDrivers {
		if d.Name == pool.Driver {
			driver = &d
			break
		}
	}

	if driver == nil || driver.Name == "cephobject" {
		return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: CSI does not support storage driver %q", pool.Driver)
	}

	// Reject request for immediate binding of local volumes.
	// We need to know which node will consume the volume, as the volume
	// needs to be created on LXD server where that particular node is running.
	topologySegments := make(map[string]string)
	if !driver.Remote {
		var target string

		// If Immediate is set, then the external-provisioner will pass in all
		// available topologies in the cluster for the driver. For local volumes
		// this may result in unschedulable pods, as the volume will be scheduled
		// independently of the pod consuming it.
		//
		// If WaitForFirstConsumer is set, then the external-provisioner will
		// wait for the scheduler to pick a node. The topology of that selected
		// node will then be set as the first entry in "accessibility_requirements.preferred".
		// All remaining topologies are still included in the requisite and preferred fields
		// to support storage  systems that span across multiple topologies.
		if req.GetAccessibilityRequirements() != nil {
			for _, topology := range req.GetAccessibilityRequirements().GetPreferred() {
				clusterMember, ok := topology.Segments[AnnotationLXDClusterMember]
				if ok {
					target = clusterMember
					break
				}
			}
		}

		// For storage backends that are topology-constrained and not globally
		// accessible from all Nodes in the cluster (e.g. local volumes), the
		// PersistentVolume may be bound or provisioned without the knowledge
		// of the Pod's scheduling requirements. This is the case when volume
		// binding mode is set to "Immediate", which will most likely result in
		// pod being unschedulable.
		//
		// See: https://kubernetes.io/docs/concepts/storage/storage-classes/#volume-binding-mode
		topologySegments[AnnotationLXDClusterMember] = target
	}

	vol, _, err := client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to retrieve storage volume %q from pool %q: %v", volName, poolName, err)
	}

	if vol != nil {
		// Volume already exists. Return successful response if it matches
		// the requested parameters to ensure idempotency.
		volSize := vol.Config["size"]
		if volSize == "" {
			return nil, status.Errorf(codes.AlreadyExists, "CreateVolume: Volume with the same name %s but no size already exist", volName)
		}

		volSizeBytes, err := strconv.ParseInt(volSize, 10, 64)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to parse volume size %q for volume %q in storage pool %q: %v", volSize, volName, poolName, err)
		}

		if volSizeBytes != sizeBytes {
			return nil, status.Errorf(codes.AlreadyExists, "CreateVolume Volume with the same name %s but with different size already exist", volName)
		}

		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      volumeID,
				CapacityBytes: sizeBytes,
				VolumeContext: parameters,
				ContentSource: contentSource,
				AccessibleTopology: []*csi.Topology{
					{
						Segments: topologySegments,
					},
				},
			},
		}, nil
	}

	if contentSource != nil {
		var sourcePoolName string
		var sourceVolName string

		switch contentSource.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			return nil, status.Error(codes.Unimplemented, "CreateVolume: Volume snapshot source is not supported yet")
		case *csi.VolumeContentSource_Volume:
			sourceVolID := contentSource.GetVolume().VolumeId
			sourcePoolName, sourceVolName, err = splitVolumeID(sourceVolID)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: %v", err)
			}

			sourceVol, _, err := client.GetStoragePoolVolume(sourcePoolName, "custom", sourceVolName)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to retrieve source snapshot: %v", err)
			}

			// Check if the existing snapshot matches the volume requirements.
			if sourceVol.ContentType != contentType {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Content type %q of volume %q does not match the requested volume content type %q", sourceVol.ContentType, sourceVolName, contentType)
			}

			sourceVolSize := sourceVol.Config["size"]
			if sourceVolSize == "" {
				return nil, status.Errorf(codes.FailedPrecondition, "CreateVolume: Cannot determine size of the existing volume %q: Size is not configured", sourceVolName)
			}

			sourceVolSizeBytes, err := strconv.ParseInt(sourceVolSize, 10, 64)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to parse size %q of the existing volume %q: %v", sourceVolSize, sourceVolName, err)
			}

			if sourceVolSizeBytes > sizeBytes {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Volume size %d is lower then the existing volume size %d", sizeBytes, sourceVolSizeBytes)
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Unsupported volume content source %q", contentSource.String())
		}

		// Create volume from a copy.
		req := api.DevLXDStorageVolumesPost{
			Name:        volName,
			Type:        "custom", // Only custom volumes can be managed by the CSI.
			ContentType: contentType,
			Source: api.StorageVolumeSource{
				Type: api.SourceTypeCopy,
				Pool: sourcePoolName,
				Name: sourceVolName,
			},
			DevLXDStorageVolumePut: api.DevLXDStorageVolumePut{
				Description: c.driver.VolumeDescription(),
				Config: map[string]string{
					"size": fmt.Sprintf("%d", sizeBytes),
				},
			},
		}

		err = client.CreateStoragePoolVolume(poolName, req)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to create volume %q in storage pool %q from volume %q in storage pool %q: %v", volName, poolName, sourceVolName, sourcePoolName, err)
		}
	} else {
		// Volume source content is not provided. Create a new volume.
		req := api.DevLXDStorageVolumesPost{
			Name:        volName,
			Type:        "custom", // Only custom volumes can be managed by the CSI.
			ContentType: contentType,
			DevLXDStorageVolumePut: api.DevLXDStorageVolumePut{
				Description: c.driver.VolumeDescription(),
				Config: map[string]string{
					"size": fmt.Sprintf("%d", sizeBytes),
				},
			},
		}

		err = client.CreateStoragePoolVolume(poolName, req)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to create volume %q in storage pool %q: %v", volName, poolName, err)
		}
	}

	// Set additional parameters to the volume for later use.
	parameters[ParameterStorageDriver] = driver.Name

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: sizeBytes,
			VolumeContext: parameters,
			ContentSource: contentSource,
			AccessibleTopology: []*csi.Topology{
				{
					Segments: topologySegments,
				},
			},
		},
	}, nil
}

func (c *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	client := c.driver.devLXD

	poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "DeleteVolume: %v", err)
	}

	unlock, err := locking.Lock(ctx, req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolume: Failed to obtain lock %q: %v", req.VolumeId, err)
	}

	defer unlock()

	// Delete storage volume. If volume does not exist, we consider
	// the operation successful.
	err = client.DeleteStoragePoolVolume(poolName, "custom", volName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(codes.Internal, "DeleteVolume: Failed to delete volume %q from storage pool %q: %v", volName, poolName, err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	client := c.driver.devLXD

	poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ControllerPublishVolume: %v", err)
	}

	contentType := ParseContentType(req.VolumeCapability)
	if contentType == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume: Volume capability must specify either block or filesystem access type")
	}

	unlock, err := locking.Lock(ctx, req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: Failed to obtain lock %q: %v", req.VolumeId, err)
	}

	defer unlock()

	// Get existing storage pool volume.
	_, _, err = client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, status.Errorf(codes.NotFound, "ControllerPublishVolume: Volume %q not found in storage pool %q", volName, poolName)
		}

		return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: Failed to retrieve volume %q from storage pool %q: %v", volName, poolName, err)
	}

	// Attach volume to the instance.
	volDevice := map[string]string{
		"source": volName,
		"pool":   poolName,
		"type":   "disk",
	}

	if contentType == "filesystem" {
		// For filesystem volumes, provide the path where the volume is mounted.
		volDevice["path"] = filepath.Join(driverFileSystemMountPath, volName)
	}

	err = client.CreateInstanceDevice(req.NodeId, volDevice)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusConflict) {
			// If device already exists, ensure it is the expected device.
			dev, _, err := client.GetInstanceDevice(req.NodeId, volName)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: %v", err)
			}

			if dev["source"] != volName || dev["pool"] != poolName || dev["type"] != "disk" || (contentType == "filesystem" && dev["path"] != filepath.Join(driverFileSystemMountPath, volName)) {
				return nil, status.Errorf(codes.AlreadyExists, "ControllerPublishVolume: Device %q already exists on node %q but does not match expected parameters", volName, req.NodeId)
			}
		} else {
			return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: Failed to attach volume %q: %v", volName, err)
		}
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (c *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	client := c.driver.devLXD

	_, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ControllerUnpublishVolume: %v", err)
	}

	unlock, err := locking.Lock(ctx, req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerUnpublishVolume: Failed to obtain lock %q: %v", req.VolumeId, err)
	}

	defer unlock()

	// Detach volume.
	// If volume attachment does not exist, consider the operation successful.
	err = client.DeleteInstanceDevice(req.NodeId, volName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(codes.Internal, "ControllerUnpublishVolume: Failed to detach volume %q: %v", volName, err)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}
