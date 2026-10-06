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

var _ = ginkgo.DescribeTableSubtree("[Volume expansion]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Online FS volume expansion",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping volume expansion test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer).
				WithVolumeExpansion(true)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create PVC for 64MiB volume.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithAccessModes(corev1.ReadWriteOncePod).
				WithVolumeMode(corev1.PersistentVolumeFilesystem).
				WithSize("64Mi")
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())

			// Ensure Pod is running and PVC is bound.
			pod.WaitReady(ctx)
			pvc.WaitBound(ctx)

			// Increase PVC size to 128MiB.
			pvc = pvc.WithSize("128Mi")
			pvc.Patch(ctx)
			pvc.WaitResize(ctx)

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Offline block volume expansion",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping volume expansion test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate).
				WithVolumeExpansion(true)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create PVC with immediate binding, but do not attach it to any pod.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithAccessModes(corev1.ReadWriteOncePod).
				WithVolumeMode(corev1.PersistentVolumeBlock).
				WithSize("64Mi")
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Ensure PVC is bound.
			pvc.WaitBound(ctx)

			// Increase PVC size to 128MiB.
			pvc = pvc.WithSize("128Mi")
			pvc.Patch(ctx)
			pvc.WaitResize(ctx)

			// Cleanup.
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Fail online block volume expansion and succeed once PVC is detached",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping volume expansion test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer).
				WithVolumeExpansion(true)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithAccessModes(corev1.ReadWriteOncePod).
				WithVolumeMode(corev1.PersistentVolumeBlock).
				WithSize("64Mi")
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())

			// Ensure Pod is running and PVC is bound.
			pod.WaitReady(ctx)
			pvc.WaitBound(ctx)

			// Increase PVC size to 128MiB.
			pvc = pvc.WithSize("128Mi")
			pvc.Patch(ctx)

			// Ensure online resize fails because volume is attached to a pod.
			pvc.WaitCondition(ctx, corev1.PersistentVolumeClaimControllerResizeError, corev1.ConditionTrue)

			// Delete Pod.
			pod.Delete(ctx)

			// Ensure offline resize succeeds once volume is detached.
			pvc.WaitResize(ctx)

			// Cleanup.
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers())
