package e2e

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

var _ = ginkgo.DescribeTableSubtree("[Volume binding mode]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Create a volume with binding mode Immediate",
		func(ctx ginkgo.SpecContext) {
			requiresStandaloneLXD()

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Ensure the pod is running and both PVCs are bound.
			pvc.WaitBound(ctx)

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())

			// Ensure the pod is running.
			pod.WaitReady(ctx)

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Create a volume with binding mode WaitForFirstConsumer",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())

			// Ensure the pod is running and the PVC is bound.
			pod.WaitReady(ctx)
			pvc.WaitBound(ctx)

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Create a pod with block and FS volumes",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping volume expansion test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvcFS := specs.NewPersistentVolumeClaim(cfg, "pvc-fs", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeFilesystem)
			pvcFS.Create(ctx)
			defer pvcFS.ForceDelete(context.Background())

			// Create Block PVC.
			pvcBlock := specs.NewPersistentVolumeClaim(cfg, "pvc-block", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock)
			pvcBlock.Create(ctx)
			defer pvcBlock.ForceDelete(context.Background())

			// Create a pod that uses both PVCs.
			pod := specs.NewPod(cfg, "pod", namespace).
				WithPVC(pvcFS, "/mnt/test").
				WithPVC(pvcBlock, "/dev/vda42")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())

			// Ensure the pod is running and both PVCs are bound.
			pod.WaitReady(ctx)
			pvcFS.WaitBound(ctx)
			pvcBlock.WaitBound(ctx)

			// Cleanup.
			pod.Delete(ctx)
			pvcFS.Delete(ctx)
			pvcBlock.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers())
