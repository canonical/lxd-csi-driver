package specs

import (
	"context"
	"maps"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/canonical/lxd-csi-driver/internal/driver"
)

// StorageClass represents a Kubernetes StorageClass.
type StorageClass struct {
	storagev1.StorageClass
	client *kubernetes.Clientset
}

// NewStorageClass creates a new StorageClass definition with the given name
// and target LXD storage pool.
func NewStorageClass(client *kubernetes.Clientset, namePrefix string, lxdStoragePool string) StorageClass {
	defaultReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	defaultVolumeBindingMode := storagev1.VolumeBindingWaitForFirstConsumer

	manifest := storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: generateName(namePrefix),
		},
		Parameters: map[string]string{
			"storagePool": lxdStoragePool,
		},
		Provisioner:       driver.DefaultDriverName,
		VolumeBindingMode: &defaultVolumeBindingMode,
		ReclaimPolicy:     &defaultReclaimPolicy,
	}

	return StorageClass{manifest, client}
}

// PrettyName returns the string consisting of StorageClass's name.
func (sc StorageClass) PrettyName() string {
	return prettyName(sc.Namespace, sc.Name)
}

// WithParameters allows setting additional parameters for the StorageClass.
func (sc StorageClass) WithParameters(params map[string]string) StorageClass {
	if sc.Parameters == nil {
		sc.Parameters = make(map[string]string)
	}

	maps.Copy(sc.Parameters, params)
	return sc
}

// WithVolumeBindingMode sets the volume binding mode for the StorageClass.
func (sc StorageClass) WithVolumeBindingMode(mode storagev1.VolumeBindingMode) StorageClass {
	sc.VolumeBindingMode = &mode
	return sc
}

// WithReclaimPolicy sets the reclaim policy for the StorageClass.
func (sc StorageClass) WithReclaimPolicy(mode corev1.PersistentVolumeReclaimPolicy) StorageClass {
	sc.ReclaimPolicy = &mode
	return sc
}

// WithDefault marks the storage class as default.
func (sc StorageClass) WithDefault(isDefault bool) StorageClass {
	if sc.Annotations == nil {
		sc.Annotations = make(map[string]string)
	}

	key := "storageclass.kubernetes.io/is-default-class"
	if isDefault {
		sc.Annotations[key] = "true"
	} else {
		delete(sc.Annotations, key)
	}

	return sc
}

// State returns the actual state of the StorageClass.
func (sc StorageClass) State(ctx context.Context) (*storagev1.StorageClass, error) {
	return sc.client.StorageV1().StorageClasses().Create(ctx, &sc.StorageClass, metav1.CreateOptions{})
}

// Create creates the StorageClass in the Kubernetes cluster.
func (sc StorageClass) Create(ctx context.Context) {
	ginkgo.By("Create StorageClass " + sc.PrettyName())
	_, err := sc.client.StorageV1().StorageClasses().Create(ctx, &sc.StorageClass, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create StorageClass %q", sc.PrettyName())
}

// delete deletes the StorageClass from the Kubernetes cluster.
func (sc StorageClass) delete(ctx context.Context, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	return sc.client.StorageV1().StorageClasses().Delete(ctx, sc.Name, *opts)
}

// Delete deletes the StorageClass from the Kubernetes cluster.
func (sc StorageClass) Delete(ctx context.Context) {
	ginkgo.By("Delete StorageClass " + sc.PrettyName())
	err := sc.delete(ctx, nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to delete StorageClass %q", sc.PrettyName())
}

// ForceDelete forcefully deletes the StorageClass from the Kubernetes cluster.
// It sets the grace period to 0 seconds to immediately remove the class.
// This is useful for cleanup.
func (sc StorageClass) ForceDelete(ctx context.Context) {
	opts := &metav1.DeleteOptions{
		GracePeriodSeconds: new(int64),
	}

	_ = sc.delete(ctx, opts)
}
