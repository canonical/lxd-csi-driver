package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/canonical/lxd-csi-driver/test/testutils"
)

func TestE2e(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	// Configure default polling intervals and timeouts.
	gomega.SetDefaultEventuallyPollingInterval(2 * time.Second)
	gomega.SetDefaultEventuallyTimeout(120 * time.Second)
	gomega.SetDefaultConsistentlyPollingInterval(2 * time.Second)
	gomega.SetDefaultConsistentlyDuration(20 * time.Second)
	gomega.EnforceDefaultTimeoutsWhenUsingContexts()

	ginkgo.RunSpecs(t, "E2e Suite")
}

var _ = ginkgo.BeforeEach(func(ctx ginkgo.SpecContext) {
	waitContainersReady(ctx, testutils.GetKubernetesClient(testutils.GetClientConfig()), "lxd-csi")
})

var _ = ginkgo.AfterEach(func() {
	// Provide useful information when test fails.
	rep := ginkgo.CurrentSpecReport()
	if rep.Failed() {
		// Ensure we do not hang waiting for logs.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		config := testutils.GetClientConfig()
		client := testutils.GetKubernetesClient(config)
		printControllerLogs(ctx, client, "lxd-csi", "lxd-csi-controller", rep.StartTime)
		printNodeLogs(ctx, client, "lxd-csi", "lxd-csi-node", rep.StartTime)
	}
})
