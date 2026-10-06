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

var _ = ginkgo.DescribeTableSubtree("[Volume read/write]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Write and read FS volume",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Set custom security context to ensure Kubelet mounts the volume with
			// read and write permissions for non-root users.
			id := int64(2000)
			podSecurityContext := &corev1.PodSecurityContext{
				FSGroup:   &id,
				RunAsUser: &id,
			}

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test").WithSecurityContext(podSecurityContext)
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			// Write to the volume.
			path := "/mnt/test/test.txt"
			msg := []byte("This is a test of an attached FS volume.")
			err := pod.WriteFile(ctx, path, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadFile(ctx, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Write and read block volume",
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
			msg := []byte("This is a test of an attached block volume.")
			err := pod.WriteDevice(ctx, dev, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadDevice(ctx, dev, len(msg))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Volume data should be retained when only pod is recreated",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod.
			pod1 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod1.Create(ctx)
			defer pod1.ForceDelete(context.Background())
			pod1.WaitReady(ctx)

			// Write to the volume.
			path := "/mnt/test/test.txt"
			msg := []byte("Hello, LXD CSI!")
			err := pod1.WriteFile(ctx, path, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod1.ReadFile(ctx, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Recreate the pod.
			pod1.Delete(ctx)

			pod2 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod2.Create(ctx)
			defer pod2.ForceDelete(context.Background())

			pod2.WaitReady(ctx)
			pvc.WaitBound(ctx)

			// Ensure the data is still there.
			data, err = pod2.ReadFile(ctx, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Cleanup.
			pod2.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers())
