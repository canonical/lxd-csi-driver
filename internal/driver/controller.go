package driver

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/shared/api"
)

type controllerServer struct {
	driver *Driver

	// Must be embedded for forward compatibility.
	csi.UnimplementedControllerServer
}

// NewControllerServer returns a new instance of the CSI controller server.
func NewControllerServer(driver *Driver) *controllerServer {
	return &controllerServer{
		driver: driver,
	}
}

// ControllerGetCapabilities returns the capabilities of the controller server.
func (c *controllerServer) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: c.driver.controllerCapabilities,
	}, nil
}

// CreateVolume creates a new volume in the LXD storage pool.
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
	var target string
	topologySegments := make(map[string]string)
	if !driver.Remote {
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
		if target != "" {
			topologySegments[AnnotationLXDClusterMember] = target

			// Only set the target when LXD is clustered.
			if c.driver.isClustered {
				client = client.UseTarget(target)
			}
		}
	}

	volumeID := getVolumeID(target, poolName, volName)

	unlock, err := locking.Lock(ctx, volumeID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to obtain lock %q: %v", volumeID, err)
	}

	defer unlock()

	vol, _, err := client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to retrieve storage volume %q from pool %q: %v", volName, poolName, err)
	}

	if vol != nil {
		return nil, status.Errorf(codes.AlreadyExists, "CreateVolume: Volume with the same name %q already exists", volName)
	}

	if contentSource != nil {
		return nil, status.Error(codes.Unimplemented, "CreateVolume: Volume source is not supported yet")
	}

	// Volume source content is not provided. Create a new volume.
	poolReq := api.DevLXDStorageVolumesPost{
		Name:        volName,
		Type:        "custom", // Only custom volumes can be managed by the CSI.
		ContentType: contentType,
		DevLXDStorageVolumePut: api.DevLXDStorageVolumePut{
			Description: c.driver.VolumeDescription(),
			Config: map[string]string{
				"size": strconv.FormatInt(sizeBytes, 10),
			},
		},
	}

	err = client.CreateStoragePoolVolume(poolName, poolReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to create volume %q in storage pool %q: %v", volName, poolName, err)
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

// DeleteVolume deletes a volume from the LXD storage pool.
func (c *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	client := c.driver.devLXD

	target, poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "DeleteVolume: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
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

// ControllerPublishVolume attaches an existing LXD custom volume to a node.
// If the volume is already attached, the operation is considered successful.
func (c *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	client := c.driver.devLXD

	target, poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ControllerPublishVolume: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
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

	inst, etag, err := client.GetInstance(req.NodeId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: %v", err)
	}

	dev, ok := inst.Devices[volName]
	if ok {
		// If the device already exists, ensure it matches the expected parameters.
		if dev["type"] != "disk" || dev["source"] != volName || dev["pool"] != poolName {
			return nil, status.Errorf(codes.AlreadyExists, "ControllerPublishVolume: Device %q already exists on node %q but does not match expected parameters", volName, req.NodeId)
		}

		return &csi.ControllerPublishVolumeResponse{}, nil
	}

	reqInst := api.DevLXDInstancePut{
		Devices: map[string]map[string]string{
			volName: {
				"source": volName,
				"pool":   poolName,
				"type":   "disk",
			},
		},
	}

	if contentType == "filesystem" {
		// For filesystem volumes, provide the path where the volume is mounted.
		reqInst.Devices[volName]["path"] = filepath.Join(driverFileSystemMountPath, volName)
	}

	err = client.UpdateInstance(req.NodeId, reqInst, etag)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerPublishVolume: Failed to attach volume %q: %v", volName, err)
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}
