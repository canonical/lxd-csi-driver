package e2e

import (
	"context"
	"os"
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
)

func TestE2e(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	// Configure default polling intervals and timeouts.
	gomega.SetDefaultEventuallyPollingInterval(1 * time.Second)
	gomega.SetDefaultEventuallyTimeout(20 * time.Second)
	gomega.SetDefaultConsistentlyPollingInterval(1 * time.Second)
	gomega.SetDefaultConsistentlyDuration(20 * time.Second)

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

func getTestLXDStoragePool() string {
	path := os.Getenv("LXD_STORAGE_POOL")
	gomega.Expect(path).NotTo(gomega.BeEmpty(), "LXD_STORAGE_POOL environment variable must be set")
	return path
}

var _ = ginkgo.AfterEach(func() {
	// Provide useful information when test fails.
	rep := ginkgo.CurrentSpecReport()
	if rep.Failed() {
		// Ensure we do not hang waiting for logs.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, client := createClient()
		printControllerLogs(ctx, client, "lxd-csi", "lxd-csi-controller", rep.StartTime)
		printNodeLogs(ctx, client, "lxd-csi", "lxd-csi-node", rep.StartTime)
	}
})

var _ = ginkgo.Describe("[Volume binding mode] ", func() {
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		_, client = createClient()
	})

	ginkgo.It("Create a volume with binding mode Immediate", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
			WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
		sc.Create(ctx)
		defer sc.ForceDelete(ctx)

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).WithStorageClassName(sc.Name)
		pvc.Create(ctx)
		defer pvc.ForceDelete(ctx)

		// Ensure the pod is running and both PVCs are bound.
		pvc.WaitBound(ctx)

		// Create a pod that uses the PVC.
		pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod.Create(ctx)
		defer pod.ForceDelete(ctx)

		// Ensure the pod is running.
		pod.WaitReady(ctx)

		// Cleanup.
		pod.Delete(ctx)
		pvc.Delete(ctx)
	}, ginkgo.SpecTimeout(5*time.Minute))

	ginkgo.It("Create a volume with binding mode WaitForFirstConsumer", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
			WithVolumeBindingMode(storagev1.VolumeBindingWaitForFirstConsumer)
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
			WithStorageClassName(sc.Name)
		pvc.Create(ctx)
		defer pvc.ForceDelete(context.Background())

		// Ensure the PVC is pending state and is not bound yet.
		state, err := pvc.State(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Eventually(state.Status.Phase).To(gomega.Equal(corev1.ClaimPending), "PVC should not be bound yet")

		// Create a pod that uses the PVC.
		pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod.Create(ctx)
		defer pod.ForceDelete(context.Background())

		// Ensure the pod is running and the PVC is bound.
		pod.WaitReady(ctx)
		pvc.WaitBound(ctx)

		// Cleanup.
		pod.Delete(ctx)
		pvc.Delete(ctx)
	}, ginkgo.SpecTimeout(5*time.Minute))

	ginkgo.It("Create a pod with block and FS volumes", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool())
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvcFS := specs.NewPersistentVolumeClaim(client, "pvc-fs", namespace).
			WithStorageClassName(sc.Name).
			WithVolumeMode(corev1.PersistentVolumeFilesystem)
		pvcFS.Create(ctx)
		defer pvcFS.ForceDelete(context.Background())

		// Create Block PVC.
		pvcBlock := specs.NewPersistentVolumeClaim(client, "pvc-block", namespace).
			WithStorageClassName(sc.Name).
			WithVolumeMode(corev1.PersistentVolumeBlock)
		pvcBlock.Create(ctx)
		defer pvcBlock.ForceDelete(context.Background())

		// Create a pod that uses both PVCs.
		pod := specs.NewPod(client, "pod", namespace).
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
	}, ginkgo.SpecTimeout(5*time.Minute))
})

var _ = ginkgo.Describe("[Volume read/write]", func() {
	var cfg *rest.Config
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg, client = createClient()
	})

	ginkgo.It("Write and read FS volume", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool())
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
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
		pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test").WithSecurityContext(podSecurityContext)
		pod.Create(ctx)
		defer pod.ForceDelete(context.Background())
		pod.WaitReady(ctx)

		// Write to the volume.
		path := "/mnt/test/test.txt"
		msg := []byte("This is a test of an attached FS volume.")
		err := pod.WriteFile(ctx, cfg, path, msg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Read back the data.
		data, err := pod.ReadFile(ctx, cfg, path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).To(gomega.Equal(msg))
	}, ginkgo.SpecTimeout(5*time.Minute))

	ginkgo.It("Write and read block volume", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool())
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create block PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
			WithStorageClassName(sc.Name).
			WithVolumeMode(corev1.PersistentVolumeBlock)
		pvc.Create(ctx)
		defer pvc.ForceDelete(context.Background())

		// Create a pod that uses the PVC.
		dev := "/dev/vda42"
		pod := specs.NewPod(client, "pod", namespace).WithPVC(pvc, dev)
		pod.Create(ctx)
		defer pod.ForceDelete(context.Background())
		pod.WaitReady(ctx)

		// Write to the volume.
		msg := []byte("This is a test of an attached FS volume.")
		err := pod.WriteDevice(ctx, cfg, dev, msg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Read back the data.
		data, err := pod.ReadDevice(ctx, cfg, dev, len(msg))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).To(gomega.Equal(msg))

		// Cleanup.
		pod.Delete(ctx)
		pvc.Delete(ctx)
	}, ginkgo.SpecTimeout(5*time.Minute))
})

var _ = ginkgo.Describe("[Volume release]", func() {
	var cfg *rest.Config
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg, client = createClient()
	})

	ginkgo.It("Volume data should be retained when only pod is recreated", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool())
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).
			WithStorageClassName(sc.Name)
		pvc.Create(ctx)
		defer pvc.ForceDelete(context.Background())

		// Create a pod.
		pod1 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod1.Create(ctx)
		defer pod1.ForceDelete(context.Background())
		pod1.WaitReady(ctx)

		// Write to the volume.
		path := "/mnt/test/test.txt"
		msg := []byte("Hello, LXD CSI!")
		err := pod1.WriteFile(ctx, cfg, path, msg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Read back the data.
		data, err := pod1.ReadFile(ctx, cfg, path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).To(gomega.Equal(msg))

		// Recreate the pod.
		pod1.Delete(ctx)

		pod2 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod2.Create(ctx)
		defer pod2.ForceDelete(context.Background())

		pod2.WaitReady(ctx)
		pvc.WaitBound(ctx)

		// Ensure the data is still there.
		data, err = pod2.ReadFile(ctx, cfg, path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(data).To(gomega.Equal(msg))

		// Cleanup.
		pod2.Delete(ctx)
		pvc.Delete(ctx)
	}, ginkgo.SpecTimeout(5*time.Minute))
})

var _ = ginkgo.Describe("[Volume access mode] ", func() {
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		_, client = createClient()
	})

	ginkgo.It("Create volume with access mode ReadWriteOnce", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
			WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).WithStorageClassName(sc.Name).WithAccessModes(corev1.ReadWriteOnce)
		pvc.Create(ctx)
		defer pvc.ForceDelete(context.Background())

		// Create a pod that uses the PVC.
		pod1 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod2 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")

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
	}, ginkgo.SpecTimeout(5*time.Minute))

	ginkgo.It("Create volume with access mode ReadWriteOncePod", func(ctx ginkgo.SpecContext) {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
			WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
		sc.Create(ctx)
		defer sc.ForceDelete(context.Background())

		// Create FS PVC.
		pvc := specs.NewPersistentVolumeClaim(client, "pvc", namespace).WithStorageClassName(sc.Name).WithAccessModes(corev1.ReadWriteOncePod)
		pvc.Create(ctx)
		defer pvc.ForceDelete(context.Background())

		// Create a pod that uses the PVC.
		pod1 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")
		pod2 := specs.NewPod(client, "pod", namespace).WithPVC(pvc, "/mnt/test")

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
	}, ginkgo.SpecTimeout(5*time.Minute))
})
