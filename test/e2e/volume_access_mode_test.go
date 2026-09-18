package e2e

import (
	"context"
	"strconv"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

// getKubernetesNodes returns the hostnames of the Kubernetes nodes, as set in the
// node label "kubernetes.io/hostname". It skips the test when the cluster has
// fewer nodes than required.
func getKubernetesNodes(ctx context.Context, cfg *rest.Config, required int) []string {
	nodes, err := testutils.GetKubernetesClient(cfg).CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to list Kubernetes nodes")

	hostnames := make([]string, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		hostnames = append(hostnames, node.Labels[corev1.LabelHostname])
	}

	if len(hostnames) < required {
		ginkgo.Skip("SKIP: Test requires at least " + strconv.Itoa(required) + " Kubernetes nodes")
	}

	return hostnames
}

var _ = ginkgo.DescribeTableSubtree("[Volume access mode]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Create volume with access mode ReadWriteOnce",
		func(ctx ginkgo.SpecContext) {
			requiresStandaloneLXD()

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
			requiresStandaloneLXD()

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

	for _, mode := range []struct {
		accessMode corev1.PersistentVolumeAccessMode
		csiMode    string
	}{
		{corev1.ReadWriteMany, "MULTI_NODE_MULTI_WRITER"},
		{corev1.ReadOnlyMany, "MULTI_NODE_READER_ONLY"},
	} {
		ginkgo.It("Reject volume with access mode "+string(mode.accessMode),
			func(ctx ginkgo.SpecContext) {
				poolName, cleanup := getTestLXDStoragePool(driver)
				defer cleanup()

				sc := specs.NewStorageClass(cfg, "sc", poolName).
					WithVolumeBindingMode(storagev1.VolumeBindingImmediate)
				sc.Create(ctx)
				defer sc.ForceDelete(context.Background())

				// Create FS PVC.
				pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
					WithStorageClassName(sc.Name).
					WithAccessModes(mode.accessMode)
				pvc.Create(ctx)
				defer pvc.ForceDelete(context.Background())

				// Ensure the volume provisioning is rejected.
				pvc.WaitEvent(ctx, "ProvisioningFailed", `Access mode "`+mode.csiMode+`" is not supported`)

				// Cleanup.
				pvc.Delete(ctx)
			},
			ginkgo.SpecTimeout(5*time.Minute),
		)
	}
}, getTestLXDStorageDrivers(), ginkgo.Label("access-mode"))
