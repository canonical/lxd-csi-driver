package specs

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

const testContainerImage = "busybox:latest"

// Pod represents a Kubernetes Pod.
type Pod struct {
	corev1.Pod
	client *kubernetes.Clientset
}

// NewPod creates a new Pod definition with the given name, namespace, and image.
func NewPod(client *kubernetes.Clientset, name string, namespace string) Pod {
	manifest := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateName(name),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "container",
					Image:   testContainerImage,
					Command: []string{"/bin/sh", "-c", "sleep infinity"},
				},
			},
		},
	}

	return Pod{manifest, client}
}

// PrettyName returns the string consisting of Pod's name and namespace.
func (p Pod) PrettyName() string {
	return prettyName(p.Namespace, p.Name)
}

// WithPVC adds a PersistentVolumeClaim to the Pod's volumes.
// The path is the mount path inside the container for filesystem volumes
// and device path inside the container for block volumes.
func (p Pod) WithPVC(pvc PersistentVolumeClaim, path string) Pod {
	p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{
		Name: pvc.Name,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvc.Name,
			},
		},
	})

	if len(p.Spec.Containers) > 0 {
		if pvc.Spec.VolumeMode == nil || *pvc.Spec.VolumeMode == corev1.PersistentVolumeFilesystem {
			// For filesystem volumes, we use the mount path.
			p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      pvc.Name,
				MountPath: path,
			})
		} else {
			// For block volumes, we use the device path.
			p.Spec.Containers[0].VolumeDevices = append(p.Spec.Containers[0].VolumeDevices, corev1.VolumeDevice{
				Name:       pvc.Name,
				DevicePath: path,
			})
		}
	}

	return p
}

// State returns the actual state of the Pod.
func (p Pod) State(ctx context.Context) (*corev1.Pod, error) {
	return p.client.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metav1.GetOptions{})
}

// Create creates the Pod in the Kubernetes cluster.
func (p Pod) Create(ctx context.Context) {
	ginkgo.By("Create Pod " + p.PrettyName())
	_, err := p.client.CoreV1().Pods(p.Namespace).Create(ctx, &p.Pod, metav1.CreateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to create Pod %q", p.PrettyName())
}

// Update updates the Pod in the Kubernetes cluster.
func (p Pod) Update(ctx context.Context) {
	ginkgo.By("Update Pod " + p.PrettyName())
	_, err := p.client.CoreV1().Pods(p.Namespace).Update(ctx, &p.Pod, metav1.UpdateOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to update Pod %q", p.PrettyName())
}

// delete deletes the Pod from the Kubernetes cluster.
func (p Pod) delete(ctx context.Context, opts *metav1.DeleteOptions) error {
	if opts == nil {
		opts = &metav1.DeleteOptions{}
	}

	return p.client.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, *opts)
}

// Delete deletes the Pod from the Kubernetes cluster.
func (p Pod) Delete(ctx context.Context) {
	ginkgo.By("Delete Pod " + p.PrettyName())
	err := p.delete(ctx, nil)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "Failed to delete Pod %q", p.PrettyName())
}

// ForceDelete forcefully deletes the Pod from the Kubernetes cluster.
// It sets the grace period to 0 seconds to immediately remove the claim.
// This is useful for cleanup.
func (p Pod) ForceDelete(ctx context.Context) {
	opts := metav1.DeleteOptions{
		// Set grace period to 0 to force delete immediately.
		GracePeriodSeconds: new(int64),
	}

	_ = p.delete(ctx, &opts)
}

// Exec executes a command in the Pod's first container.
func (p Pod) Exec(ctx context.Context, cfg *rest.Config, cmd []string) (string, error) {
	if len(p.Spec.Containers) == 0 {
		return "", fmt.Errorf("Failed to exec into Pod %q: Pod has no containers", p.Name)
	}

	return p.ExecContainer(ctx, cfg, p.Spec.Containers[0].Name, cmd)
}

// ExecContainer executes a command in the pod's container and returns stdout.
func (p Pod) ExecContainer(ctx context.Context, cfg *rest.Config, container string, cmd []string) (string, error) {
	execOpts := &corev1.PodExecOptions{
		Container: container,
		Command:   cmd,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}

	req := p.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(p.Name).
		Namespace(p.Namespace).
		SubResource("exec").
		VersionedParams(execOpts, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("Failed to exec into Pod %q: %w", p.Name, err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	opts := remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}

	err = exec.StreamWithContext(ctx, opts)
	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("Failed to exec into Pod %q: %w: %s", p.Name, err, stderr.String())
		}

		return "", fmt.Errorf("Failed to exec into Pod, %q: %w", p.Name, err)
	}

	return stdout.String(), nil
}

// WriteFile writes arbitrary bytes to a filesystem path inside the pod.
// Data is base64-encoded before sending to avoid issues with shell quoting.
func (p *Pod) WriteFile(ctx context.Context, cfg *rest.Config, path string, data []byte) error {
	ginkgo.By("Write " + strconv.Itoa(len(data)) + " bytes to file " + path + " in pod " + p.PrettyName())
	b64 := base64.StdEncoding.EncodeToString(data)
	script := fmt.Sprintf(`
set -e
echo %q | base64 -d > %q
`, b64, path)
	_, err := p.Exec(ctx, cfg, []string{"sh", "-c", script})
	return err
}

// ReadFile reads the entire contents of a file from inside the pod.
func (p *Pod) ReadFile(ctx context.Context, cfg *rest.Config, path string) ([]byte, error) {
	ginkgo.By("Read file " + path + " in pod " + p.PrettyName())
	out, err := p.Exec(ctx, cfg, []string{"sh", "-c", fmt.Sprintf("base64 %q", path)})
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.DecodeString(strings.TrimSpace(out))
}

// WriteDevice writes raw bytes to a block device inside the Pod.
// Data is base64-encoded before sending to avoid issues with shell quoting.
func (p *Pod) WriteDevice(ctx context.Context, cfg *rest.Config, device string, data []byte) error {
	ginkgo.By("Write " + strconv.Itoa(len(data)) + " bytes to device " + device + " in pod " + p.PrettyName())
	b64 := base64.StdEncoding.EncodeToString(data)
	script := fmt.Sprintf(`
set -e
echo %q | base64 -d | dd of=%q bs=1 conv=fsync,notrunc status=none
`, b64, device)

	_, err := p.Exec(ctx, cfg, []string{"sh", "-c", script})
	return err
}

// ReadDevice reads exactly n bytes from a block device inside the Pod.
func (p *Pod) ReadDevice(ctx context.Context, cfg *rest.Config, device string, n int) ([]byte, error) {
	ginkgo.By("Read " + strconv.Itoa(n) + " bytes from device " + device + " in pod " + p.PrettyName())
	script := fmt.Sprintf(`dd if=%q bs=1 count=%d status=none | base64`, device, n)
	out, err := p.Exec(ctx, cfg, []string{"sh", "-c", script})
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.DecodeString(strings.TrimSpace(out))
}

// WaitReady waits until the Pod is in the Ready state.
func (p Pod) WaitReady(ctx context.Context, timeout time.Duration) {
	ginkgo.By("Wait for Pod " + p.PrettyName() + " to be ready")
	podReady := func() bool {
		state, err := p.State(ctx)
		if err != nil {
			return false
		}

		for _, cond := range state.Status.Conditions {
			if cond.Type == corev1.PodReady {
				return cond.Status == corev1.ConditionTrue
			}
		}

		return false
	}

	gomega.Eventually(podReady).WithTimeout(timeout).Should(gomega.BeTrue(), "Pod %q is not ready after %s", p.PrettyName(), timeout)
}

// WaitRunning waits until the Pod is in the Running state.
func (p Pod) WaitRunning(ctx context.Context, timeout time.Duration) {
	ginkgo.By("Wait for Pod " + p.PrettyName() + " to be running")
	podPhase := func() corev1.PodPhase {
		state, err := p.State(ctx)
		if err != nil {
			return corev1.PodUnknown
		}

		return state.Status.Phase
	}

	gomega.Eventually(podPhase).WithTimeout(timeout).Should(gomega.Equal(corev1.PodRunning), "Pod %q is not running after %s", p.PrettyName(), timeout)
}

// WaitGone waits until the Pod is no longer present in the Kubernetes cluster.
func (p Pod) WaitGone(ctx context.Context, timeout time.Duration) {
	ginkgo.By("Wait for Pod " + p.PrettyName() + " to be gone")
	podGone := func() bool {
		_, err := p.State(ctx)
		return apierrors.IsNotFound(err)
	}

	gomega.Eventually(podGone).WithTimeout(timeout).Should(gomega.BeTrue(), "Pod %q is not gone after %s", p.PrettyName(), timeout)
}
