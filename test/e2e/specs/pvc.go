package specs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// PersistentVolumeClaim represents a Kubernetes PersistentVolumeClaim.
type PersistentVolumeClaim struct {
	corev1.PersistentVolumeClaim
	client *kubernetes.Clientset
}

// NewPersistentVolumeClaim creates a new PersistentVolumeClaim with the given name and
// namespace. By default, the size is set to 64MiB and access mode is set to ReadWriteOnce.
func NewPersistentVolumeClaim(client *kubernetes.Clientset, name string, namespace string) PersistentVolumeClaim {
	manifest := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name),
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("64Mi"),
				},
			},
		},
	}

	return PersistentVolumeClaim{manifest, client}
}

// PrettyName returns the string consisting of PersistentVolumeClaim's name and namespace.
func (pvc PersistentVolumeClaim) PrettyName() string {
	return prettyName(pvc.Namespace, pvc.Name)
}

// WithVolumeMode sets the volume mode for the PersistentVolumeClaim.
// It can be either Filesystem or Block.
func (pvc PersistentVolumeClaim) WithVolumeMode(mode corev1.PersistentVolumeMode) PersistentVolumeClaim {
	pvc.Spec.VolumeMode = &mode
	return pvc
}

// WithAccessModes sets the access modes for the PersistentVolumeClaim.
func (pvc PersistentVolumeClaim) WithAccessModes(accessModes ...corev1.PersistentVolumeAccessMode) PersistentVolumeClaim {
	pvc.Spec.AccessModes = accessModes
	return pvc
}

// WithStorageClassName sets the storage class name for the PersistentVolumeClaim.
func (pvc PersistentVolumeClaim) WithStorageClassName(storageClassName string) PersistentVolumeClaim {
	pvc.Spec.StorageClassName = &storageClassName
	return pvc
}

// WithSize sets the size of the PersistentVolumeClaim.
// The size can be specified in bytes or in binary SI format.
func (pvc PersistentVolumeClaim) WithSize(size string) PersistentVolumeClaim {
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse(size)
	return pvc
}

// State returns the actual state of the PersistentVolumeClaim.
func (pvc PersistentVolumeClaim) State(ctx context.Context) (*corev1.PersistentVolumeClaim, error) {
	return pvc.client.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(ctx, pvc.Name, metav1.GetOptions{})
}

// Create creates the PersistentVolumeClaim in the Kubernetes cluster.
func (pvc PersistentVolumeClaim) Create(ctx context.Context) {
	ginkgo.By("Create PersistentVolumeClaim " + pvc.PrettyName())
	_, err := pvc.client.CoreV1().PersistentVolumeClaims(pvc.Namespace).Create(ctx, &pvc.PersistentVolumeClaim, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create PVC %q", pvc.PrettyName())
}

// Patch updates the PersistentVolumeClaim in the Kubernetes cluster.
func (pvc PersistentVolumeClaim) Patch(ctx context.Context) {
	ginkgo.By("Update PersistentVolumeClaim " + pvc.PrettyName())
	bytes, err := json.Marshal(pvc.PersistentVolumeClaim)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to marshal PVC %q into JSON", pvc.PrettyName())
	_, err = pvc.client.CoreV1().PersistentVolumeClaims(pvc.Namespace).Patch(ctx, pvc.Name, types.StrategicMergePatchType, bytes, metav1.PatchOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to patch PVC %q", pvc.PrettyName())
}

// delete deletes the PersistentVolumeClaim from the Kubernetes cluster.
func (pvc PersistentVolumeClaim) delete(ctx context.Context, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	return pvc.client.CoreV1().PersistentVolumeClaims(pvc.Namespace).Delete(ctx, pvc.Name, *opts)
}

// Delete deletes the PersistentVolumeClaim from the Kubernetes cluster.
func (pvc PersistentVolumeClaim) Delete(ctx context.Context) {
	ginkgo.By("Delete PersistentVolumeClaim " + pvc.PrettyName())
	err := pvc.delete(ctx, nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to delete PVC %q", pvc.PrettyName())
}

// ForceDelete forcefully deletes the PersistentVolumeClaim from the Kubernetes cluster.
// It sets the grace period to 0 seconds to immediately remove the claim.
// This is useful for cleanup.
func (pvc PersistentVolumeClaim) ForceDelete(ctx context.Context) {
	opts := &metav1.DeleteOptions{
		GracePeriodSeconds: new(int64),
	}

	_ = pvc.delete(ctx, opts)
}

// WaitBound waits until the PersistentVolumeClaim is bound to a PersistentVolume.
func (pvc PersistentVolumeClaim) WaitBound(ctx context.Context, timeout time.Duration) {
	ginkgo.By("Wait for PersistentVolumeClaim " + pvc.PrettyName() + " to be bound")
	pvcPhase := func() corev1.PersistentVolumeClaimPhase {
		state, err := pvc.State(ctx)
		if err != nil {
			return ""
		}

		return state.Status.Phase
	}

	gomega.Eventually(pvcPhase).WithTimeout(timeout).Should(gomega.Equal(corev1.ClaimBound), "PVC %q is not bound after %s", pvc.PrettyName(), timeout)
}

// WaitSize waits until the PersistentVolumeClaim is resized to desired size.
func (pvc PersistentVolumeClaim) WaitSize(ctx context.Context, size string, timeout time.Duration) {
	ginkgo.By("Wait size of PersistentVolumeClaim " + pvc.PrettyName() + " to be " + size)
	pvcSize := func() string {
		state, err := pvc.State(ctx)
		if err != nil {
			return ""
		}

		v, ok := state.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			return ""
		}

		return v.String()
	}

	gomega.Eventually(pvcSize).WithTimeout(timeout).Should(gomega.Equal(size), "PVC %q size is not %q after %s", pvc.PrettyName(), size, timeout)
}

// WaitGone waits until the PVC is no longer present in the Kuberentes cluster.
func (pvc PersistentVolumeClaim) WaitGone(ctx context.Context, timeout time.Duration) {
	ginkgo.By("Wait for PersistentVolumeClaim " + pvc.PrettyName() + " to be gone")
	podGone := func() bool {
		_, err := pvc.State(ctx)
		return apierrors.IsNotFound(err)
	}

	gomega.Eventually(podGone).WithTimeout(timeout).Should(gomega.BeTrue(), "PVC %q is not gone after %s", pvc.PrettyName(), timeout)
}
