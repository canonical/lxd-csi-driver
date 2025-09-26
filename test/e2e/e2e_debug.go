package e2e

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

func printControllerLogs(ctx context.Context, client *kubernetes.Clientset, namespace string, name string, since time.Time) {
	fmt.Printf("\n=== Controller logs ===\n")

	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		fmt.Println("Failed to retrieve CSI controller Deployment:", err)
		return
	}

	selector := labels.Set(dep.Spec.Selector.MatchLabels).AsSelector().String()
	err = getPodLogsBySelector(ctx, client, namespace, selector, since)
	if err != nil {
		fmt.Println("Failed to retrieve CSI controller logs:", err)
	}
}

func printNodeLogs(ctx context.Context, client *kubernetes.Clientset, namespace string, name string, since time.Time) {
	fmt.Printf("\n===    Node logs    ===\n")

	ds, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		fmt.Println("Failed to retrieve CSI node DaemonSet:", err)
		return
	}

	selector := labels.Set(ds.Spec.Selector.MatchLabels).AsSelector().String()
	err = getPodLogsBySelector(ctx, client, namespace, selector, since)
	if err != nil {
		fmt.Println("Failed to retrieve CSI node logs:", err)
	}
}

// Print merged, chronological logs for all containers and all pods that match the provided selector.
func getPodLogsBySelector(ctx context.Context, cs *kubernetes.Clientset, namespace string, selector string, since time.Time) error {
	type logLine struct {
		t   time.Time
		msg string
	}

	pl, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}

	if len(pl.Items) == 0 {
		return fmt.Errorf("No pods found for selector %q", selector)
	}

	sinceTime := metav1.NewTime(since)
	maxLines := int64(10) // Limit max number of lines per container to avoid flooding the output.

	var lines []logLine
	for _, p := range pl.Items {
		// Discover all containers and sort by name.
		containers := []string{}
		for _, c := range p.Spec.Containers {
			containers = append(containers, c.Name)
		}

		// Fetch logs for each container.
		for _, c := range containers {
			req := cs.CoreV1().Pods(namespace).GetLogs(p.Name, &corev1.PodLogOptions{
				Container:  c,
				SinceTime:  &sinceTime,
				TailLines:  &maxLines,
				Timestamps: true,
			})

			rc, err := req.Stream(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to stream log for %s/%s: %v\n", p.Name, c, err)
				continue
			}

			// Parse the log lines.
			func() {
				defer rc.Close()

				sc := bufio.NewScanner(rc)
				for sc.Scan() {
					raw := sc.Text()

					// Split off the RFC3339Nano timestamp prefix.
					i := strings.IndexByte(raw, ' ')
					if i <= 0 {
						continue
					}

					// Parse the timestamp to allow chronological sorting.
					ts, err := time.Parse(time.RFC3339Nano, raw[:i])
					if err != nil {
						continue
					}

					// Store the timestamp and the line without timestamp.
					lines = append(lines, logLine{
						t:   ts,
						msg: raw[i+1:],
					})
				}

				_ = sc.Err()
			}()
		}

		if len(lines) > 0 {
			// Sort all lines chronologically.
			// This helps understand the sequence of events when multiple containers are involved.
			sort.Slice(lines, func(i, j int) bool {
				return lines[i].t.Before(lines[j].t)
			})

			fmt.Printf("==> Logs for Pod %s\n", p.Name)
			for _, l := range lines {
				fmt.Println(l.msg)
			}

			// Reset for next pod.
			lines = []logLine{}
		}
	}

	return nil
}
