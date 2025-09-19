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

var _ = ginkgo.Describe("[Volume binding mode] ", func() {
	var ctx context.Context
	var client *kubernetes.Clientset
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		ctx = context.Background()
		_, client = createClient()
	})

	ginkgo.It("Create a volume with binding mode Immediate", func() {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
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
	})

	ginkgo.It("Create a volume with binding mode WaitForFirstConsumer", func() {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool()).
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
	})

	ginkgo.It("Create a pod with block and FS volumes", func() {
		sc := specs.NewStorageClass(client, "sc", getTestLXDStoragePool())
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
	})
})
