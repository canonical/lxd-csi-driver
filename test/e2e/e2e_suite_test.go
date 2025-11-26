package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func TestE2e(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	// Configure default polling intervals and timeouts.
	gomega.SetDefaultEventuallyPollingInterval(2 * time.Second)
	gomega.SetDefaultEventuallyTimeout(120 * time.Second)
	gomega.SetDefaultConsistentlyPollingInterval(2 * time.Second)
	gomega.SetDefaultConsistentlyDuration(20 * time.Second)
	gomega.EnforceDefaultTimeoutsWhenUsingContexts()

	ginkgo.RunSpecs(t, "E2e Suite")
}

// getTestLXDStorageDrivers returns the list of LXD storage drivers to be used for testing.
// It reads the TEST_LXD_STORAGE_DRIVERS environment variable, which should contain a comma-separated
// list of drivers. If the variable is not set, it defaults to ["dir"].
func getTestLXDStorageDrivers() []ginkgo.TableEntry {
	entries := []ginkgo.TableEntry{}

	drivers := os.Getenv("TEST_LXD_STORAGE_DRIVERS")
	if drivers == "" {
		drivers = "dir"
	}

	for driver := range strings.SplitSeq(drivers, ",") {
		entries = append(entries, ginkgo.Entry("Driver "+driver, driver))
	}

	return entries
}

// getTestLXDStoragePool creates a new LXD storage pool with the given driver for testing purposes.
// It returns the name of the created storage pool and a cleanup function to delete it after use.
func getTestLXDStoragePool(driver string) (poolName string, cleanup func()) {
	poolName = "lxd-csi-" + driver + "-" + testutils.GenerateStringN(5)

	client, err := lxd.ConnectLXDUnix("", nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to connect to local LXD over Unix socket: %v", err)

	config := make(map[string]string)
	if driver != "dir" {
		config["size"] = "512MiB"
		config["volume.size"] = "128MiB"
	}

	if driver == "lvm" {
		config["lvm.use_thinpool"] = "false"
	}

	req := api.StoragePoolsPost{
		Name:   poolName,
		Driver: driver,
		StoragePoolPut: api.StoragePoolPut{
			Config:      config,
			Description: "LXD CSI Driver E2E Test Storage Pool",
		},
	}

	err = client.CreateStoragePool(req)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create storage pool %q with driver %q: %v", req.Name, req.Driver, err)

	cleanup = func() {
		_ = client.DeleteStoragePool(req.Name)
		client.Disconnect()
	}

	return poolName, cleanup
}

var _ = ginkgo.AfterEach(func() {
	// Provide useful information when test fails.
	rep := ginkgo.CurrentSpecReport()
	if rep.Failed() {
		// Ensure we do not hang waiting for logs.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		config := testutils.GetClientConfig()
		client := testutils.GetKubernetesClient(config)
		printControllerLogs(ctx, client, "lxd-csi", "lxd-csi-controller", rep.StartTime)
		printNodeLogs(ctx, client, "lxd-csi", "lxd-csi-node", rep.StartTime)
	}
})

var _ = ginkgo.DescribeTableSubtree("[Volume binding mode]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Create a volume with binding mode Immediate",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Ensure the pod is running and both PVCs are bound.
			pvc.WaitBound(ctx)

			// Create a pod that uses the PVC.
			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)

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
}, getTestLXDStorageDrivers())

var _ = ginkgo.DescribeTableSubtree("[Volume release]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

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

var _ = ginkgo.DescribeTableSubtree("[Volume access mode] ", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Create volume with access mode ReadWriteOnce",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).WithStorageClassName(sc.Name).WithAccessModes(corev1.ReadWriteOnce)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			pod1 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod2 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")

			pod1.Create(ctx)
			defer pod1.ForceDelete(context.Background())

			pod2.Create(ctx)
			defer pod2.ForceDelete(context.Background())

			// Ensure the pods are running.
			pod1.WaitReady(ctx)
			pod2.WaitReady(ctx)

			// Ensure PVC is bound.
			pvc.WaitBound(ctx)

			pod1.Delete(ctx)
			pod2.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)

	ginkgo.It("Create volume with access mode ReadWriteOncePod",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(cfg, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).WithStorageClassName(sc.Name).WithAccessModes(corev1.ReadWriteOncePod)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			// Create a pod that uses the PVC.
			pod1 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod2 := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")

			pod1.Create(ctx)
			defer pod1.ForceDelete(context.Background())

			// Ensure Pod is running and PVC is bound.
			pod1.WaitReady(ctx)
			pvc.WaitBound(ctx)

			pod2.Create(ctx)
			defer pod2.ForceDelete(context.Background())

			// Ensure the second pod does not become ready because
			// PVC is already bound to the first pod.
			pod2.EnsureNotRunning(ctx, 10*time.Second)

			// Cleanup.
			pod1.Delete(ctx)
			pod2.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(5*time.Minute),
	)
}, getTestLXDStorageDrivers())

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
			err := pod1.WriteDevice(ctx, filePath, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Remove the pod.
			pod1.Delete(ctx)

			// Create a cloned PVC from the original PVC.
			pvcClone := specs.NewPersistentVolumeClaim(cfg, "pvc-cloned", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeFilesystem).
				WithSource(pvc.Name)

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
			data, err := pod2.ReadDevice(ctx, filePath, len(msg))
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
				WithSource(pvc.Name)

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
