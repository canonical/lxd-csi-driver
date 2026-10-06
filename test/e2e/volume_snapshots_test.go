package e2e

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

var _ = ginkgo.DescribeTableSubtree("[Volume snapshots]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Create and delete volume snapshot",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate).
				WithVolumeExpansion(true)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			vsc := specs.NewVolumeSnapshotClass(cfg, "sc")
			vsc.Create(ctx)
			defer vsc.ForceDelete(context.Background())

			// Create new PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithAccessModes(corev1.ReadWriteOncePod).
				WithVolumeMode(corev1.PersistentVolumeFilesystem).
				WithSize("64Mi")
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Ensure PVC is bound.
			pvc.WaitBound(ctx)

			// Create volume snapshot.
			snapshot := specs.NewVolumeSnapshot(cfg, "snapshot", namespace, pvc.Name).
				WithVolumeSnapshotClassName(vsc.Name)
			snapshot.Create(ctx)
			defer snapshot.ForceDelete(context.Background())

			// Ensure the snapshot is ready to use.
			snapshot.WaitReadyToUse(ctx)

			// Cleanup.
			snapshot.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Snapshot as volume source",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer).
				WithVolumeExpansion(true)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			vsc := specs.NewVolumeSnapshotClass(cfg, "vsc")
			vsc.Create(ctx)
			defer vsc.ForceDelete(context.Background())

			// Create new PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithSize("64Mi")
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			mntPath := "/mnt/test"
			filePath := "/mnt/test/test.txt"
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, mntPath)
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			// Write to the volume.
			msg := []byte("Initial content.")
			err := pod.WriteFile(ctx, filePath, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data to confirm it has been written.
			data, err := pod.ReadFile(ctx, filePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Create volume snapshot.
			snapshot := specs.NewVolumeSnapshot(cfg, "snapshot", namespace, pvc.Name).
				WithVolumeSnapshotClassName(vsc.Name)
			snapshot.Create(ctx)
			defer snapshot.ForceDelete(context.Background())
			snapshot.WaitReadyToUse(ctx)

			// Modify content to ensure it differs from the snapshot taken before.
			err = pod.WriteFile(ctx, filePath, []byte("Modified content."))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Create a new PVC that uses the snapshot as a source.
			restoredPVC := specs.NewPersistentVolumeClaim(cfg, "pvc-restored", namespace).
				WithStorageClassName(sc.Name).
				WithSourceSnapshot(snapshot.Name).
				WithSize("64Mi")
			restoredPVC.Create(ctx)
			defer restoredPVC.ForceDelete(context.Background())

			// Recreate a pod and use restored PVC for a new one.
			pod.Delete(ctx)
			pod = specs.NewPod(cfg, "pod", namespace).WithPVC(restoredPVC, mntPath)
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			// Make sure PVC is bound (the restore from snapshot has completed)
			// before deleting a snapshot. Otherwise, Kubernetes will block the
			// deletion request with an error "snapshot is in use".
			restoredPVC.WaitBound(ctx)

			// Remove no longer needed snapshot and parent PVC.
			snapshot.Delete(ctx)
			pvc.Delete(ctx)

			// Read the data to confirm volume was successfully restored from a snapshot.
			data, err = pod.ReadFile(ctx, filePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod.Delete(ctx)
			restoredPVC.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers(), ginkgo.Label("snapshots"))
