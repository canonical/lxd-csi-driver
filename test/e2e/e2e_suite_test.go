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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

func TestE2e(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "E2e Suite")
}

func createClient() (*rest.Config, *kubernetes.Clientset) {
	path := os.Getenv("K8S_KUBECONFIG_PATH")
	gomega.Expect(path).NotTo(gomega.BeEmpty(), "K8S_KUBECONFIG_PATH environment variable must be set")

	config, err := clientcmd.BuildConfigFromFlags("", path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	client, err := kubernetes.NewForConfig(config)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return config, client
}

// getTestLXDStorageDrivers returns the list of LXD storage drivers to be used for testing.
// It reads the TEST_LXD_STORAGE_DRIVERS environment variable, which should contain a comma-separated
// list of drivers. If the variable is not set, it defaults to ["dir"].
func getTestLXDStorageDrivers() []ginkgo.TableEntry {
	entries := []ginkgo.TableEntry{}

	drivers := os.Getenv("TEST_LXD_STORAGE_DRIVERS")
	if drivers == "" {
		drivers = "lvm"
	}

	for driver := range strings.SplitSeq(drivers, ",") {
		entries = append(entries, ginkgo.Entry("Driver "+driver, driver))
	}

	return entries
}

// getTestLXDStoragePool creates a new LXD storage pool with the given driver for testing purposes.
// It returns the name of the created storage pool and a cleanup function to delete it after use.
func getTestLXDStoragePool(driver string) (poolName string, cleanup func()) {
	poolName = "lxd-csi-" + driver + testutils.GenerateName("")

	client, err := lxd.ConnectLXDUnix("", nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to connect to local LXD over Unix socket: %v", err)

	config := make(map[string]string)
	if driver != "dir" {
		config["size"] = "128MiB"
	}

	if driver == "lvm" {
		// XXX: Temporary workaround LVM thin pool issue.
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

var _ = ginkgo.Describe("[Volume binding mode]", func() {
	var ctx context.Context
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		ctx = context.Background()
		_, client = createClient()
	})

	ginkgo.DescribeTable("Create a volume with binding mode Immediate",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Ensure the pod is running and both PVCs are bound.
			pvc.WaitBound(ctx, 30*time.Second)

			// Create a pod that uses the PVC.
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)

			// Ensure the pod is running.
			pod.WaitReady(ctx, 60*time.Second)
		},
		getTestLXDStorageDrivers(),
	)

	ginkgo.DescribeTable("Create a volume with binding mode WaitForFirstConsumer",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName).
				WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Ensure the PVC is pending state and is not bound yet.
			state, err := pvc.State(ctx)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Eventually(state.Status.Phase).To(gomega.Equal(corev1.ClaimPending), "PVC should not be bound yet")

			// Create a pod that uses the PVC.
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)

			// Ensure the pod is running and the PVC is bound.
			pod.WaitReady(ctx, 60*time.Second)
			pvc.WaitBound(ctx, 30*time.Second)
		},
		getTestLXDStorageDrivers(),
	)

	ginkgo.DescribeTable("Create a pod with block and FS volumes",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvcFS := specs.NewPersistentVolumeClaim(client, "pvc-fs", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeFilesystem)
			pvcFS.Create(ctx)
			defer pvcFS.Delete(ctx)

			// Create Block PVC.
			pvcBlock := specs.NewPersistentVolumeClaim(client, "pvc-block", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock)
			pvcBlock.Create(ctx)
			defer pvcBlock.Delete(ctx)

			// Create a pod that uses both PVCs.
			pod := specs.NewPod(client, "pod", namespace).
				WithPVC(pvcFS, "/mnt/test").
				WithPVC(pvcBlock, "/dev/vda42")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)

			// Ensure the pod is running and both PVCs are bound.
			pod.WaitReady(ctx, 60*time.Second)
			pvcFS.WaitBound(ctx, 30*time.Second)
			pvcBlock.WaitBound(ctx, 30*time.Second)
		},
		getTestLXDStorageDrivers(),
	)
})

var _ = ginkgo.Describe("[Volume read/write]", func() {
	var ctx context.Context
	var cfg *rest.Config
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		ctx = context.Background()
		cfg, client = createClient()
	})

	ginkgo.DescribeTable("Write and read FS volume",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Create a pod that uses the PVC.
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)
			pod.WaitReady(ctx, 60*time.Second)

			// Write to the volume.
			path := "/mnt/test/test.txt"
			msg := []byte("This is a test of an attached FS volume.")
			err := pod.WriteFile(ctx, cfg, path, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadFile(ctx, cfg, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))
		},
		getTestLXDStorageDrivers(),
	)

	ginkgo.DescribeTable("Write and read block volume",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create block PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Create a pod that uses the PVC.
			dev := "/dev/vda42"
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, dev)
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)
			pod.WaitReady(ctx, 30*time.Second)

			// Write to the volume.
			msg := []byte("This is a test of an attached FS volume.")
			err := pod.WriteDevice(ctx, cfg, dev, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadDevice(ctx, cfg, dev, len(msg))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))
		},
		getTestLXDStorageDrivers(),
	)
})

var _ = ginkgo.Describe("[Volume release]", func() {
	var ctx context.Context
	var cfg *rest.Config
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		ctx = context.Background()
		cfg, client = createClient()
	})

	ginkgo.DescribeTable("Volume data should be retained when only pod is recreated",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Create a pod.
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)
			pod.WaitReady(ctx, 60*time.Second)

			// Write to the volume.
			path := "/mnt/test/test.txt"
			msg := []byte("Hello, LXD CSI!")
			err := pod.WriteFile(ctx, cfg, path, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadFile(ctx, cfg, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Recreate the pod.
			pod.Delete(ctx)
			pod.WaitGone(ctx, 60*time.Second)
			pod.Create(ctx)
			pod.WaitReady(ctx, 30*time.Second)
			pvc.WaitBound(ctx, 30*time.Second)

			// Ensure the data is still there.
			data, err = pod.ReadFile(ctx, cfg, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))
		},
		getTestLXDStorageDrivers(),
	)

	ginkgo.DescribeTable("Volume data should be gone when PVC is recreated",
		func(driver string) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			sc := specs.NewStorageClass(client, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(ctx)

			// Create FS PVC.
			pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(ctx)

			// Create a pod.
			pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(ctx)
			pod.WaitReady(ctx, 60*time.Second)

			// Write to the volume.
			path := "/mnt/test/test.txt"
			msg := []byte("Hello, LXD CSI!")
			err := pod.WriteFile(ctx, cfg, path, msg)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Read back the data.
			data, err := pod.ReadFile(ctx, cfg, path)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(data).To(gomega.Equal(msg))

			// Recreate the pod and PVC.
			pod.Delete(ctx)
			pod.WaitGone(ctx, 60*time.Second)
			pvc.Delete(ctx)
			pvc.WaitGone(ctx, 30*time.Second)
			pvc.Create(ctx)
			pod.Create(ctx)
			pod.WaitReady(ctx, 60*time.Second)
			pvc.WaitBound(ctx, 30*time.Second)

			// Ensure the data is no longer there.
			_, err = pod.ReadFile(ctx, cfg, path)
			gomega.Expect(err).To(gomega.HaveOccurred())
		},
		getTestLXDStorageDrivers(),
	)
})
