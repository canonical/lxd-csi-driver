package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/canonical/lxd-csi-driver/test/e2e/specs"
	"github.com/canonical/lxd-csi-driver/test/testutils"
)

// pvcRef references the PersistentVolumeClaim a volume was provisioned for.
type pvcRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// kubeletVolumeStats is a subset of the volume statistics reported by the Kubelet Summary API.
// Kubelet populates these from the driver's NodeGetVolumeStats procedure, therefore they reflect
// exactly what the driver has reported.
type kubeletVolumeStats struct {
	CapacityBytes  int64 `json:"capacityBytes"`
	UsedBytes      int64 `json:"usedBytes"`
	AvailableBytes int64 `json:"availableBytes"`
	Inodes         int64 `json:"inodes"`
	InodesUsed     int64 `json:"inodesUsed"`
	InodesFree     int64 `json:"inodesFree"`

	PVCRef *pvcRef `json:"pvcRef,omitempty"`
}

// kubeletSummary is a subset of the Kubelet Summary API response.
type kubeletSummary struct {
	Pods []struct {
		Volume []kubeletVolumeStats `json:"volume"`
	} `json:"pods"`
}

// getVolumeStats returns the volume statistics that Kubelet on the given node reports for the given
// PersistentVolumeClaim. Nil is returned if the statistics are not reported (yet).
func getVolumeStats(ctx context.Context, client *kubernetes.Clientset, nodeName string, namespace string, pvcName string) (*kubeletVolumeStats, error) {
	raw, err := client.CoreV1().RESTClient().Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix("stats", "summary").
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve stats summary from node %q: %w", nodeName, err)
	}

	var summary kubeletSummary
	err = json.Unmarshal(raw, &summary)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse stats summary from node %q: %w", nodeName, err)
	}

	for _, pod := range summary.Pods {
		for _, volume := range pod.Volume {
			if volume.PVCRef != nil && volume.PVCRef.Name == pvcName && volume.PVCRef.Namespace == namespace {
				return &volume, nil
			}
		}
	}

	return nil, nil
}

// waitVolumeStats waits until Kubelet reports the volume statistics for the given PersistentVolumeClaim.
// Kubelet collects the statistics periodically (once per minute by default), therefore they are not
// available immediately after the pod becomes ready.
func waitVolumeStats(ctx context.Context, client *kubernetes.Clientset, nodeName string, namespace string, pvcName string) *kubeletVolumeStats {
	var stats *kubeletVolumeStats

	ginkgo.By("Wait for Kubelet to report volume stats for PVC " + namespace + "/" + pvcName)
	gomega.Eventually(func() (*kubeletVolumeStats, error) {
		var err error
		stats, err = getVolumeStats(ctx, client, nodeName, namespace, pvcName)
		return stats, err
	}).WithTimeout(3*time.Minute).WithPolling(10*time.Second).ShouldNot(gomega.BeNil(),
		"Kubelet did not report volume stats for PVC %q on node %q", pvcName, nodeName)

	return stats
}

var _ = ginkgo.DescribeTableSubtree("[Volume stats]", func(driver string) {
	var cfg *rest.Config
	var namespace = "default"

	ginkgo.BeforeEach(func() {
		cfg = testutils.GetClientConfig()
	})

	ginkgo.It("Report stats for FS volume",
		func(ctx ginkgo.SpecContext) {
			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			client := testutils.GetKubernetesClient(cfg)

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/mnt/test")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			state, err := pod.State(ctx)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(state.Spec.NodeName).NotTo(gomega.BeEmpty())

			nodeName := state.Spec.NodeName
			stats := waitVolumeStats(ctx, client, nodeName, namespace, pvc.Name)

			// Filesystem volumes report the capacity.
			gomega.Expect(stats.CapacityBytes).To(gomega.BeNumerically(">", 0))

			// The inode usage is reported only by filesystems that preallocate the
			// inodes. Btrfs allocates them dynamically, therefore it reports no
			// inode counts at all.
			if driver == "btrfs" {
				gomega.Expect(stats.Inodes).To(gomega.BeZero())
			} else {
				gomega.Expect(stats.Inodes).To(gomega.BeNumerically(">", 0))
				gomega.Expect(stats.InodesUsed + stats.InodesFree).To(gomega.Equal(stats.Inodes))
			}

			// Blocks reserved for a privileged user are not available, but are not
			// reported as used either. Therefore, the sum of the used and available
			// capacity does not have to match the total capacity.
			gomega.Expect(stats.UsedBytes + stats.AvailableBytes).To(gomega.BeNumerically("<=", stats.CapacityBytes))

			if driver == "dir" {
				// The "dir" storage driver cannot bound the volume size, as the volume
				// is a plain directory on the host filesystem. Therefore the reported
				// capacity belongs to the filesystem that backs the storage pool and
				// no assumptions can be made about the reported values.
				return
			}

			// Reported capacity must not exceed the requested volume size.
			// Btrfs is an exception, as it bounds the volume with a qgroup limit, which
			// is not reflected in the filesystem statistics. Its volumes therefore
			// report the capacity of the entire storage pool.
			if driver != "btrfs" {
				gomega.Expect(stats.CapacityBytes).To(gomega.BeNumerically("<=", 64*1024*1024))
			}

			// Write a known amount of data and ensure the reported usage grows.
			// Random data is used because storage drivers may support compression,
			// in which case an easily compressible content would occupy no space.
			usedBytes := stats.UsedBytes
			_, err = pod.Exec(ctx, []string{"sh", "-c", "dd if=/dev/urandom of=/mnt/test/data bs=1M count=8 conv=fsync status=none"})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("Wait for Kubelet to report increased volume usage")
			gomega.Eventually(func() (int64, error) {
				stats, err := getVolumeStats(ctx, client, nodeName, namespace, pvc.Name)
				if err != nil {
					return 0, err
				}

				if stats == nil {
					return 0, fmt.Errorf("Kubelet does not report volume stats for PVC %q", pvc.Name)
				}

				return stats.UsedBytes, nil
			}).WithTimeout(3*time.Minute).WithPolling(10*time.Second).Should(gomega.BeNumerically(">=", usedBytes+4*1024*1024),
				"Kubelet did not report increased volume usage after writing 8MiB")

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(10*time.Minute),
	)

	ginkgo.It("Report stats for block volume",
		func(ctx ginkgo.SpecContext) {
			if driver == "dir" {
				ginkgo.Skip("Skipping block volume stats test for 'dir' driver, as it does not support volume size")
			}

			poolName, cleanup := getTestLXDStoragePool(driver)
			defer cleanup()

			client := testutils.GetKubernetesClient(cfg)

			sc := specs.NewStorageClass(cfg, "sc", poolName)
			sc.Create(ctx)
			defer sc.ForceDelete(context.Background())

			pvc := specs.NewPersistentVolumeClaim(cfg, "pvc", namespace).
				WithStorageClassName(sc.Name).
				WithVolumeMode(corev1.PersistentVolumeBlock)
			pvc.Create(ctx)
			defer pvc.ForceDelete(context.Background())

			pod := specs.NewPod(cfg, "pod", namespace).WithPVC(pvc, "/dev/vda42")
			pod.Create(ctx)
			defer pod.ForceDelete(context.Background())
			pod.WaitReady(ctx)

			state, err := pod.State(ctx)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(state.Spec.NodeName).NotTo(gomega.BeEmpty())

			stats := waitVolumeStats(ctx, client, state.Spec.NodeName, namespace, pvc.Name)

			// Raw block volumes are not formatted by the driver, therefore only the
			// total size of the block device is reported.
			gomega.Expect(stats.CapacityBytes).To(gomega.BeNumerically(">", 0))
			gomega.Expect(stats.CapacityBytes).To(gomega.BeNumerically("<=", 64*1024*1024))
			gomega.Expect(stats.UsedBytes).To(gomega.BeZero())
			gomega.Expect(stats.AvailableBytes).To(gomega.BeZero())
			gomega.Expect(stats.Inodes).To(gomega.BeZero())

			// Cleanup.
			pod.Delete(ctx)
			pvc.Delete(ctx)
		},
		ginkgo.SpecTimeout(10*time.Minute),
	)
}, getTestLXDStorageDrivers())
