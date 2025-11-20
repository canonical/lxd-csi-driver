package driver

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/lxd-csi-driver/internal/errors"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/units"
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
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: %v", err)
	}

	// Construct volume name.
	// The volume name is constructed from a prefix and the remaining UUID of [req.Name]
	// after the first dash, with all dashes removed from the UUID. This shortens the
	// volume name while still keeping it unique.
	volPrefix, volUUID, found := strings.Cut(req.Name, "-")
	if !found {
		return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Unexpected volume name format: %q", req.Name)
	}

	// Override volume prefix if configured.
	if c.driver.volumeNamePrefix != "" {
		volPrefix = c.driver.volumeNamePrefix
	}

	volName := volPrefix + "-" + strings.ReplaceAll(volUUID, "-", "")

	contentSource := req.VolumeContentSource

	err = ValidateVolumeCapabilities(req.VolumeCapabilities...)
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
		if strings.HasPrefix(k, "csi.storage.k8s.io/") {
			// Skip standard CSI parameters.
			continue
		}

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
		return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: Failed to retrieve storage pool %q: %v", poolName, err)
	}

	// Fetch the information about storage pool driver and ensure
	// it is supported.
	state, err := client.GetState()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: %v", err)
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
	var accessibleTopology []*csi.Topology
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
			accessibleTopology = []*csi.Topology{
				{
					Segments: map[string]string{
						AnnotationLXDClusterMember: target,
					},
				},
			}

			// Only set the target when LXD is clustered.
			if c.driver.isClustered {
				client = client.UseTarget(target)
			}
		}
	}

	volumeID := getVolumeID(target, poolName, volName)

	unlock := locking.TryLock(volumeID)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "CreateVolume: Failed to obtain lock %q", volumeID)
	}

	defer unlock()

	vol, _, err := client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: Failed to retrieve storage volume %q from pool %q: %v", volName, poolName, err)
	}

	if vol != nil {
		return nil, status.Errorf(codes.AlreadyExists, "CreateVolume: Volume with the same name %q already exists", volName)
	}

	// If PVC name was passed to the driver, use it as the volume description.
	// Otherwise, use a generic description to clearly indicate the volume is managed by Kubernetes.
	volumeDescription := "Managed by Kubernetes PVC"
	pvcName := parameters[ParameterPVCName]
	if pvcName != "" {
		pvcIdentifier := pvcName

		pvcNamespace := parameters[ParameterPVCNamespace]
		if pvcNamespace != "" {
			pvcIdentifier = pvcNamespace + "/" + pvcName
		}

		volumeDescription = volumeDescription + " " + pvcIdentifier
	}

	if contentSource != nil {
		var sourcePoolName string
		var sourceVolName string
		var sourceTarget string

		switch contentSource.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			return nil, status.Error(codes.Unimplemented, "CreateVolume: Using snapshot as volume source is not supported")
		case *csi.VolumeContentSource_Volume:
			sourceVolID := contentSource.GetVolume().VolumeId
			sourceTarget, sourcePoolName, sourceVolName, err = splitVolumeID(sourceVolID)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: %v", err)
			}

			sourceClient := client
			if c.driver.isClustered {
				// Ensure source volume target is respected when LXD is clustered.
				sourceClient = sourceClient.UseTarget(sourceTarget)
			} else {
				// Clear target for non-clustered LXD deployments.
				sourceTarget = ""
			}

			// Fetch source volume.
			sourceVol, _, err := sourceClient.GetStoragePoolVolume(sourcePoolName, "custom", sourceVolName)
			if err != nil {
				return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: Failed to retrieve source volume: %v", err)
			}

			// Check if the source volume matches the volume requirements.
			if sourceVol.ContentType != contentType {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Content type %q of volume %q does not match the requested volume content type %q", sourceVol.ContentType, sourceVolName, contentType)
			}

			sourceVolSize := sourceVol.Config["size"]
			if sourceVolSize == "" {
				return nil, status.Errorf(codes.FailedPrecondition, "CreateVolume: Cannot determine size of the source volume %q: Size is not configured", sourceVolName)
			}

			sourceVolSizeBytes, err := strconv.ParseInt(sourceVolSize, 10, 64)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "CreateVolume: Failed to parse size %q of the source volume %q: %v", sourceVolSize, sourceVolName, err)
			}

			if sourceVolSizeBytes > sizeBytes {
				return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Source volume size %d is larger than the volume size %d", sourceVolSizeBytes, sizeBytes)
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Unsupported source volume content %q", contentSource.String())
		}

		// Create volume from a copy.
		poolReq := api.DevLXDStorageVolumesPost{
			Name:        volName,
			Type:        "custom", // Only custom volumes can be managed by the CSI.
			ContentType: contentType,
			Source: api.DevLXDStorageVolumeSource{
				Type:     api.SourceTypeCopy,
				Pool:     sourcePoolName,
				Name:     sourceVolName,
				Location: sourceTarget,
			},
			DevLXDStorageVolumePut: api.DevLXDStorageVolumePut{
				Description: volumeDescription,
				Config: map[string]string{
					"size": strconv.FormatInt(sizeBytes, 10),
				},
			},
		}

		op, err := client.CreateStoragePoolVolume(poolName, poolReq)
		if err == nil {
			err = op.WaitContext(ctx)
		}

		if err != nil {
			return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: Failed to create volume %q in storage pool %q from volume %q in storage pool %q: %v", volName, poolName, sourceVolName, sourcePoolName, err)
		}
	} else {
		// Volume source content is not provided. Create a new volume.
		poolReq := api.DevLXDStorageVolumesPost{
			Name:        volName,
			Type:        "custom", // Only custom volumes can be managed by the CSI.
			ContentType: contentType,
			DevLXDStorageVolumePut: api.DevLXDStorageVolumePut{
				Description: volumeDescription,
				Config: map[string]string{
					"size": strconv.FormatInt(sizeBytes, 10),
				},
			},
		}

		op, err := client.CreateStoragePoolVolume(poolName, poolReq)
		if err == nil {
			err = op.WaitContext(ctx)
		}

		if err != nil {
			return nil, status.Errorf(errors.ToGRPCCode(err), "CreateVolume: Failed to create volume %q in storage pool %q: %v", volName, poolName, err)
		}
	}

	// Set additional parameters to the volume for later use.
	parameters[ParameterStorageDriver] = driver.Name

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           volumeID,
			CapacityBytes:      sizeBytes,
			VolumeContext:      parameters,
			ContentSource:      contentSource,
			AccessibleTopology: accessibleTopology,
		},
	}, nil
}

// DeleteVolume deletes a volume from the LXD storage pool.
func (c *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "DeleteVolume: %v", err)
	}

	target, poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "DeleteVolume: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
	}

	unlock := locking.TryLock(req.VolumeId)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "DeleteVolume: Failed to obtain lock %q", req.VolumeId)
	}

	defer unlock()

	// Delete storage volume. If volume does not exist, we consider
	// the operation successful.
	op, err := client.DeleteStoragePoolVolume(poolName, "custom", volName)
	if err == nil {
		err = op.WaitContext(ctx)
	}

	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(errors.ToGRPCCode(err), "DeleteVolume: Failed to delete volume %q from storage pool %q: %v", volName, poolName, err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

// CreateSnapshot creates a snapshot of a PVC that references an existing LXD custom volume.
func (c *controllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "CreateSnapshot: %v", err)
	}

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateSnapshot: Snapshot name cannot be empty")
	}

	if req.SourceVolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateSnapshot: Source volume ID cannot be empty")
	}

	// Generate snapshot name and ID.
	// Snapshot name is constructed from the requested snapshot name by removing dashes
	// from the UUID portion. This shortens the snapshot name while keeping it unique.
	snapshotPrefix, snapshotUUID, found := strings.Cut(req.Name, "-")
	if !found {
		return nil, status.Errorf(codes.InvalidArgument, "CreateVolume: Unexpected volume name format: %q", req.Name)
	}

	snapshotName := snapshotPrefix + "-" + strings.ReplaceAll(snapshotUUID, "-", "")
	snapshotID := req.SourceVolumeId + "/" + snapshotName

	target, poolName, volName, err := splitVolumeID(req.SourceVolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "CreateSnapshot: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
	}

	unlock := locking.TryLock(snapshotID)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "CreateSnapshot: Failed to obtain lock %q", snapshotID)
	}

	defer unlock()

	_, _, err = client.GetStoragePoolVolumeSnapshot(poolName, "custom", volName, snapshotName)
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, status.Errorf(errors.ToGRPCCode(err), "CreateSnapshot: Failed to retrieve snapshot %q of volume %q from pool %q: %v", snapshotName, volName, poolName, err)
		}

		// Create snapshot of storage volume.
		snapshotReq := api.DevLXDStorageVolumeSnapshotsPost{
			Name:        snapshotName,
			Description: "Managed by Kubernetes VolumeSnapshot " + snapshotName,
		}

		// Snapshot does not exist yet. Create it.
		op, err := client.CreateStoragePoolVolumeSnapshot(poolName, "custom", volName, snapshotReq)
		if err == nil {
			err = op.WaitContext(ctx)
		}

		if err != nil {
			return nil, status.Errorf(errors.ToGRPCCode(err), "CreateSnapshot: %v", err)
		}
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     snapshotID,
			SourceVolumeId: req.SourceVolumeId,
			CreationTime:   timestamppb.Now(),
			ReadyToUse:     true,
		},
	}, nil
}

// DeleteSnapshot deletes a snapshot of an LXD custom volume.
// Missing snapshots are treated as successfully deleted.
func (c *controllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "DeleteSnapshot: %v", err)
	}

	target, poolName, volName, snapshotName, err := splitSnapshotID(req.SnapshotId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "DeleteSnapshot: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
	}

	unlock := locking.TryLock(req.SnapshotId)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "DeleteSnapshot: Failed to obtain lock %q", req.SnapshotId)
	}

	defer unlock()

	op, err := client.DeleteStoragePoolVolumeSnapshot(poolName, "custom", volName, snapshotName)
	if err == nil {
		err = op.WaitContext(ctx)
	}

	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(errors.ToGRPCCode(err), "DeleteSnapshot: %v", err)
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

// ControllerPublishVolume attaches an existing LXD custom volume to a node.
// If the volume is already attached, the operation is considered successful.
func (c *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerPublishVolume: %v", err)
	}

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

	unlock := locking.TryLock(req.VolumeId)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "ControllerPublishVolume: Failed to obtain lock %q", req.VolumeId)
	}

	defer unlock()

	// Get existing storage pool volume.
	_, _, err = client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil, status.Errorf(codes.NotFound, "ControllerPublishVolume: Volume %q not found in storage pool %q", volName, poolName)
		}

		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerPublishVolume: Failed to retrieve volume %q from storage pool %q: %v", volName, poolName, err)
	}

	inst, etag, err := client.GetInstance(req.NodeId)
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerPublishVolume: %v", err)
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
		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerPublishVolume: Failed to attach volume %q: %v", volName, err)
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

// ControllerUnpublishVolume detaches LXD custom volume from a node.
// If the volume is not attached, the operation is considered successful.
func (c *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerUnpublishVolume: %v", err)
	}

	_, _, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ControllerUnpublishVolume: %v", err)
	}

	unlock := locking.TryLock(req.VolumeId)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "ControllerUnpublishVolume: Failed to obtain lock %q", req.VolumeId)
	}

	defer unlock()

	reqInst := api.DevLXDInstancePut{
		Devices: map[string]map[string]string{
			volName: nil,
		},
	}

	// Detach volume.
	// If volume attachment does not exist, consider the operation successful.
	err = client.UpdateInstance(req.NodeId, reqInst, "")
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ControllerUnpublishVolume: Failed to detach volume %q: %v", volName, err)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ControllerExpandVolume resizes an existing LXD custom volume.
func (c *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	client, err := c.driver.DevLXDClient()
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ExpandVolume: %v", err)
	}

	target, poolName, volName, err := splitVolumeID(req.VolumeId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ExpandVolume: %v", err)
	}

	// Set target if provided and LXD is clustered.
	if target != "" && c.driver.isClustered {
		client = client.UseTarget(target)
	}

	err = ValidateVolumeCapabilities(req.VolumeCapability)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "ExpandVolume: %v", err)
	}

	unlock := locking.TryLock(req.VolumeId)
	if unlock == nil {
		return nil, status.Errorf(codes.Aborted, "ExpandVolume: Failed to obtain lock %q: %v", req.VolumeId, err)
	}

	defer unlock()

	vol, etag, err := client.GetStoragePoolVolume(poolName, "custom", volName)
	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ExpandVolume: %v", err)
	}

	oldSize := vol.Config["size"]
	if oldSize == "" {
		return nil, status.Errorf(codes.Internal, "ExpandVolume: Volume %q in storage pool %q does not have size configured", volName, poolName)
	}

	oldSizeBytes, err := strconv.ParseInt(oldSize, 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ExpandVolume: Failed to parse current volume size %q for volume %q in storage pool %q: %v", oldSize, volName, poolName, err)
	}

	newSizeBytes := req.CapacityRange.RequiredBytes

	// Volume shrinking is currently not supported by Kubernetes.
	// However, to be on the safe side, we double check that the request is
	// not trying to shrink the volume size.
	if newSizeBytes < oldSizeBytes {
		oldSizePretty := units.GetByteSizeStringIEC(oldSizeBytes, 2)
		newSizePretty := units.GetByteSizeStringIEC(newSizeBytes, 2)
		return nil, status.Errorf(codes.InvalidArgument, "ExpandVolume: Requested size %q is less than the current size %q", newSizePretty, oldSizePretty)
	}

	if newSizeBytes == oldSizeBytes {
		// Nothing to do. New size equals the already configured size.
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         newSizeBytes,
			NodeExpansionRequired: false,
		}, nil
	}

	// Expand volume.
	volReq := api.DevLXDStorageVolumePut{
		Config: map[string]string{
			"size": strconv.FormatInt(newSizeBytes, 10),
		},
	}

	op, err := client.UpdateStoragePoolVolume(poolName, "custom", volName, volReq, etag)
	if err == nil {
		err = op.WaitContext(ctx)
	}

	if err != nil {
		return nil, status.Errorf(errors.ToGRPCCode(err), "ExpandVolume: Failed to expand volume: %v", err)
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newSizeBytes,
		NodeExpansionRequired: false,
	}, nil
}
