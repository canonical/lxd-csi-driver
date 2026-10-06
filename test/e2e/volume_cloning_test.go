package e2e

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

var _ = ginkgo.DescribeTableSubtree("[Volume cloning]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Write to FS volume, clone it, and read from a new volume",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create filesystem PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeFilesystem)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			mntPath := "/mnt/test"
			filePath := "/mnt/test/test.txt"
			pod1 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, mntPath)
			pod1.Create(ctx)
			defer pod1.ForceDelete(context.Background())
			pod1.WaitReady(ctx)

			// Write to the volume.
			msg := []byte("This is a test of a cloned FS volume.")
			err := pod1.WriteFile(ctx, filePath, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Remove the pod.
			pod1.Delete(ctx)

			// Create a cloned PVC from the original PVC.
			pvcClone := specs.NewPersistentVolumeClaim(cfg, "pvc-cloned", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeFilesystem).
				WithSourcePVC(pvc.Name)

			pvcClone.Create(ctx)
			defer pvcClone.ForceDelete(context.Background())

			// Create a pod that uses the cloned PVC.
			pod2 := specs.NewPod(cfg, "pod-cloned", namespace).WithPVC(pvcClone, mntPath)
			pod2.Create(ctx)
			defer pod2.ForceDelete(context.Background())

			// Ensure the pod is running and the cloned PVC is bound.
			pod2.WaitReady(ctx)
			pvcClone.WaitBound(ctx)

			// Remove source PVC.
			pvc.Delete(ctx)

			// Read back the data from the cloned volume.
			data, err := pod2.ReadFile(ctx, filePath)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod2.Delete(ctx)
			pvcClone.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Write to block volume, clone it, and read from a new volume",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping volume expansion test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create block PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			dev := "/dev/vda42"
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, dev)
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			// Write to the volume.
			msg := []byte("This is a test of a cloned block volume.")
			err := pod.WriteDevice(ctx, dev, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Remove no longer needed pod.
			pod.Delete(ctx)

			// Create a cloned PVC from the original PVC.
			pvcClone := specs.NewPersistentVolumeClaim(cfg, "pvc-cloned", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock).
				WithSourcePVC(pvc.Name)

			pvcClone.Create(ctx)
			defer pvcClone.ForceDelete(context.Background())

			// Create a pod that uses the cloned PVC.
			pod2 := specs.NewPod(cfg, "pod-cloned", namespace).WithPVC(pvcClone, dev)
			pod2.Create(ctx)
			defer pod2.ForceDelete(context.Background())

			// Ensure the pod is running and the cloned PVC is bound.
			pod2.WaitReady(ctx)
			pvcClone.WaitBound(ctx)

			// Remove source PVC.
			pvc.Delete(ctx)

			// Read back the data from the cloned volume.
			data, err := pod2.ReadDevice(ctx, dev, len(msg))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod2.Delete(ctx)
			pvcClone.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers())
