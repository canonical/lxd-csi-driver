package specs

import (
	"context"
	"fmt"
	"maps"
	"strings"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	snapshotter "github.com/kubernetes-csi/external-snapshotter/client/v8/clientset/versioned"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/internal/driver"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

// VolumeSnapshotClass represents a Kubernetes VolumeSnapshotClass.
type VolumeSnapshotClass struct {
	snapshotv1.VolumeSnapshotClass
	k8sClient *kubernetes.Clientset
	client    *snapshotter.Clientset
}

// NewVolumeSnapshotClass creates a new VolumeSnapshotClass definition with the given name.
func NewVolumeSnapshotClass(cfg *rest.Config, namePrefix string) VolumeSnapshotClass {
	manifest := snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testutils.GenerateName(namePrefix),
		},
		Driver:         driver.DefaultDriverName,
		DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
	}

	return VolumeSnapshotClass{
		VolumeSnapshotClass: manifest,
		k8sClient:           testutils.GetKubernetesClient(cfg),
		client:              testutils.GetSnapshotterClient(cfg),
	}
}

// PrettyName returns the string consisting of VolumeSnapshotClass's name.
func (sc VolumeSnapshotClass) PrettyName() string {
	return prettyName(sc.Namespace, sc.Name)
}

// WithParameters allows setting additional parameters for the VolumeSnapshotClass.
func (sc VolumeSnapshotClass) WithParameters(params map[string]string) VolumeSnapshotClass {
	if sc.Parameters == nil {
		sc.Parameters = make(map[string]string)
	}

	maps.Copy(sc.Parameters, params)
	return sc
}

// WithDeletionPolicy sets the deletion policy for the VolumeSnapshotClass.
func (sc VolumeSnapshotClass) WithDeletionPolicy(policy snapshotv1.DeletionPolicy) VolumeSnapshotClass {
	sc.DeletionPolicy = policy
	return sc
}

// State returns the actual state of the VolumeSnapshotClass.
func (sc VolumeSnapshotClass) State(ctx context.Context) (*snapshotv1.VolumeSnapshotClass, error) {
	return sc.client.SnapshotV1().VolumeSnapshotClasses().Get(ctx, sc.Name, metav1.GetOptions{})
}

// StateString returns the state of the VolumeSnapshotClass as a string.
// This is useful to include in error messages when desired state is not achieved.
func (sc VolumeSnapshotClass) StateString(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VolumeSnapshotClass %q state:\n", sc.PrettyName())

	state, err := sc.State(ctx)
	if err != nil {
		fmt.Fprintln(&b, "- Failed to get state:", err.Error())
	} else {
		fmt.Fprintln(&b, "- Driver:", state.Driver)
		fmt.Fprintln(&b, "- DeletionPolicy:", state.DeletionPolicy)

		if len(state.Parameters) > 0 {
			fmt.Fprintf(&b, "- Parameters: %v\n", state.Parameters)
		}
	}

	return b.String()
}

// Create creates the VolumeSnapshotClass in the Kubernetes cluster.
func (sc VolumeSnapshotClass) Create(ctx context.Context) {
	ginkgo.By("Create VolumeSnapshotClass " + sc.PrettyName())
	_, err := sc.client.SnapshotV1().VolumeSnapshotClasses().Create(ctx, &sc.VolumeSnapshotClass, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create VolumeSnapshotClass %q\n%s", sc.PrettyName(), sc.StateString(ctx))
}

// delete deletes the VolumeSnapshotClass from the Kubernetes cluster.
func (sc VolumeSnapshotClass) delete(ctx context.Context, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	return sc.client.SnapshotV1().VolumeSnapshotClasses().Delete(ctx, sc.Name, *opts)
}

// Delete deletes the VolumeSnapshotClass from the Kubernetes cluster.
func (sc VolumeSnapshotClass) Delete(ctx context.Context) {
	ginkgo.By("Delete VolumeSnapshotClass " + sc.PrettyName())
	err := sc.delete(ctx, nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to delete VolumeSnapshotClass %q\n%s", sc.PrettyName(), sc.StateString(ctx))
}

// ForceDelete forcefully deletes the VolumeSnapshotClass from the Kubernetes cluster.
// It sets the grace period to 0 seconds to immediately remove the class.
// This is useful for cleanup.
func (sc VolumeSnapshotClass) ForceDelete(ctx context.Context) {
	opts := &metav1.DeleteOptions{
		GracePeriodSeconds: new(int64),
	}

	_ = sc.delete(ctx, opts)
}
