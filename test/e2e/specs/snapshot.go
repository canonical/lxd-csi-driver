package specs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	snapshotter "github.com/kubernetes-csi/external-snapshotter/client/v8/clientset/versioned"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/canonical/lxd-csi-driver/test/testutils"
)

// VolumeSnapshot represents a Kubernetes VolumeSnapshot.
type VolumeSnapshot struct {
	snapshotv1.VolumeSnapshot
	k8sClient *kubernetes.Clientset
	client    *snapshotter.Clientset
}

// NewVolumeSnapshot creates a new VolumeSnapshot with the given name and namespace.
func NewVolumeSnapshot(cfg *rest.Config, name string, namespace string, pvcName string) VolumeSnapshot {
	manifest := snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testutils.GenerateName(name),
			Namespace: namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvcName,
			},
		},
	}

	return VolumeSnapshot{
		VolumeSnapshot: manifest,
		k8sClient:      testutils.GetKubernetesClient(cfg),
		client:         testutils.GetSnapshotterClient(cfg),
	}
}

// PrettyName returns the string consisting of VolumeSnapshot's name and namespace.
func (snapshot VolumeSnapshot) PrettyName() string {
	return prettyName(snapshot.Namespace, snapshot.Name)
}

// WithVolumeSnapshotClassName sets the volume snapshot class name for the VolumeSnapshot.
func (snapshot VolumeSnapshot) WithVolumeSnapshotClassName(volumeSnapshotClassName string) VolumeSnapshot {
	snapshot.Spec.VolumeSnapshotClassName = &volumeSnapshotClassName
	return snapshot
}

// Events returns the events related to the VolumeSnapshot.
func (snapshot VolumeSnapshot) Events(ctx context.Context) (*corev1.EventList, error) {
	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.kind", "VolumeSnapshot"),
		fields.OneTermEqualSelector("involvedObject.name", snapshot.Name),
		fields.OneTermEqualSelector("involvedObject.namespace", snapshot.Namespace),
	)

	return snapshot.k8sClient.CoreV1().Events(snapshot.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: selector.String(),
	})
}

// State returns the actual state of the VolumeSnapshot.
func (snapshot VolumeSnapshot) State(ctx context.Context) (*snapshotv1.VolumeSnapshot, error) {
	return snapshot.client.SnapshotV1().VolumeSnapshots(snapshot.Namespace).Get(ctx, snapshot.Name, metav1.GetOptions{})
}

// StateString returns the state of the VolumeSnapshot as a string.
// This is useful to include in error messages when desired state is not achieved.
func (snapshot VolumeSnapshot) StateString(ctx context.Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Snapshot %q state:\n", snapshot.PrettyName())

	state, err := snapshot.State(ctx)
	if err != nil {
		fmt.Fprintln(&b, "- Failed to get state:", err.Error())
	} else if state.Status != nil {
		fmt.Fprintln(&b, "- BoundVolumeSnapshotContentName:", state.Status.BoundVolumeSnapshotContentName)

		if state.Status.ReadyToUse != nil {
			fmt.Fprintln(&b, "- Phase:", ptr.Deref(state.Status.ReadyToUse, true))
		}

		if state.Status.Error != nil {
			fmt.Fprintf(&b, "- Error: %v\n", state.Status.Error.Message)
		}
	}

	events, err := snapshot.Events(ctx)
	if err != nil {
		fmt.Fprintln(&b, "- Failed to get events:", err.Error())
	} else {
		for _, e := range events.Items {
			fmt.Fprintf(&b, "- Event %s %s: %s\n", e.Type, e.Reason, e.Message)
		}
	}

	return b.String()
}

// Create creates the VolumeSnapshot in the Kubernetes cluster.
func (snapshot VolumeSnapshot) Create(ctx context.Context) {
	ginkgo.By("Create VolumeSnapshot " + snapshot.PrettyName())
	_, err := snapshot.client.SnapshotV1().VolumeSnapshots(snapshot.Namespace).Create(ctx, &snapshot.VolumeSnapshot, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create snapshot %q", snapshot.PrettyName())
}

// Patch updates the VolumeSnapshot in the Kubernetes cluster.
func (snapshot *VolumeSnapshot) Patch(ctx context.Context) {
	ginkgo.By("Update VolumeSnapshot " + snapshot.PrettyName())
	bytes, err := json.Marshal(snapshot.VolumeSnapshot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to marshal snapshot %q into JSON", snapshot.PrettyName())
	_, err = snapshot.client.SnapshotV1().VolumeSnapshots(snapshot.Namespace).Patch(ctx, snapshot.Name, types.StrategicMergePatchType, bytes, metav1.PatchOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to patch snapshot %q\n%s", snapshot.PrettyName(), snapshot.StateString(ctx))
}

// delete deletes the VolumeSnapshot from the Kubernetes cluster.
func (snapshot *VolumeSnapshot) delete(ctx context.Context, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	return snapshot.client.SnapshotV1().VolumeSnapshots(snapshot.Namespace).Delete(ctx, snapshot.Name, *opts)
}

// Delete deletes the VolumeSnapshot from the Kubernetes cluster.
func (snapshot *VolumeSnapshot) Delete(ctx context.Context) {
	ginkgo.By("Delete VolumeSnapshot " + snapshot.PrettyName())

	err := snapshot.delete(ctx, nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to delete snapshot %q\n%s", snapshot.PrettyName(), snapshot.StateString(ctx))
	snapshot.WaitGone(ctx)
}

// ForceDelete forcefully deletes the VolumeSnapshot from the Kubernetes cluster.
// It sets the grace period to 0 seconds to immediately remove the snapshot.
// This is useful for cleanup.
func (snapshot VolumeSnapshot) ForceDelete(ctx context.Context) {
	opts := &metav1.DeleteOptions{
		GracePeriodSeconds: new(int64),
	}

	_ = snapshot.delete(ctx, opts)
}

// WaitReadyToUse waits until the VolumeSnapshot is ready to be used.
func (snapshot VolumeSnapshot) WaitReadyToUse(ctx context.Context) {
	ginkgo.By("Wait for VolumeSnapshot " + snapshot.PrettyName() + " to be ready to use")
	isReady := func(ctx context.Context) bool {
		state, err := snapshot.State(ctx)
		if err != nil || state == nil || state.Status == nil || state.Status.ReadyToUse == nil {
			return false
		}

		return *state.Status.ReadyToUse
	}

	gomega.Eventually(isReady).WithContext(ctx).Should(gomega.BeTrue(), "Snapshot %q is not ready to use\n%s", snapshot.PrettyName(), snapshot.StateString(ctx))
}

// WaitGone waits until the snapshot is no longer present in the Kubernetes cluster.
func (snapshot VolumeSnapshot) WaitGone(ctx context.Context) {
	ginkgo.By("Wait for VolumeSnapshot " + snapshot.PrettyName() + " to be gone")
	snapshotGone := func(ctx context.Context) bool {
		_, err := snapshot.State(ctx)
		return apierrors.IsNotFound(err)
	}

	gomega.Eventually(snapshotGone).WithContext(ctx).Should(gomega.BeTrue(), "Snapshot %q is not gone\n%s", snapshot.PrettyName(), snapshot.StateString(ctx))
}
