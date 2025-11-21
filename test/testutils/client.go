package testutils

import (
	"os"

	snapshotter "github.com/kubernetes-csi/external-snapshotter/client/v8/clientset/versioned"
	"github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// GetClientConfig reads the Kubeconfig file path from the K8S_KUBECONFIG_PATH
// environment variable and returns a Kubernetes REST config.
//
// Note: The expected environment variable is intentionally named "K8S_KUBECONFIG_PATH"
// instead of the conventional "KUBECONFIG". This prevents accidentally running the tests
// against the developer's default kubeconfig.
func GetClientConfig() *rest.Config {
	path := os.Getenv("K8S_KUBECONFIG_PATH")
	gomega.Expect(path).NotTo(gomega.BeEmpty(), "K8S_KUBECONFIG_PATH environment variable must be set")

	config, err := clientcmd.BuildConfigFromFlags("", path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// Increase client-side queries per second (QPS) and burst
	// to avoid rate limit failures in tests.
	config.QPS = 50
	config.Burst = 100

	return config
}

// GetKubernetesClient creates and returns a Kubernetes clientset for the given REST config.
func GetKubernetesClient(config *rest.Config) *kubernetes.Clientset {
	client, err := kubernetes.NewForConfig(config)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return client
}

// GetSnapshotterClient creates and returns an external snapshotter clientset for the given REST config.
func GetSnapshotterClient(config *rest.Config) *snapshotter.Clientset {
	client, err := snapshotter.NewForConfig(config)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return client
}
