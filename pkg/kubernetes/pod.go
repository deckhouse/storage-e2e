/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// WaitForPodsStatus waits for pods to reach a specific status
func WaitForPodsStatus(ctx context.Context, clientset *kubernetes.Clientset, namespace, labelSelector, status string, expectedCount int, maxAttempts int, interval time.Duration) error {
	attempt := 0
	lastLogTime := time.Now()

	for {
		// Check context cancellation first
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pods status %s: %w", status, ctx.Err())
		default:
		}

		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		readyCount := 0
		failedPods := []string{}
		pendingPods := []string{}

		for _, pod := range pods.Items {
			podPhase := string(pod.Status.Phase)
			if podPhase == status || (status == "Completed" && pod.Status.Phase == corev1.PodSucceeded) {
				readyCount++
			} else if podPhase == "Failed" || podPhase == "Error" {
				failedPods = append(failedPods, fmt.Sprintf("%s (reason: %s)", pod.Name, pod.Status.Reason))
			} else if podPhase == "Pending" {
				pendingPods = append(pendingPods, pod.Name)
			}
		}

		// Log progress every 30 seconds
		if time.Since(lastLogTime) >= 30*time.Second {
			logger.Progress("Waiting for pods: %d/%d in status %s (attempt %d)", readyCount, expectedCount, status, attempt)
			if len(failedPods) > 0 {
				logger.Warn("Failed pods: %v", failedPods)
			}
			if len(pendingPods) > 0 && len(pendingPods) <= 10 {
				logger.Debug("Pending pods: %v", pendingPods)
			} else if len(pendingPods) > 10 {
				logger.Debug("Pending pods: %d pods still pending", len(pendingPods))
			}
			lastLogTime = time.Now()
		}

		if readyCount >= expectedCount {
			return nil
		}

		// Check for failures - if too many pods failed, return error early
		if len(failedPods) > 0 && len(failedPods) > expectedCount/10 {
			return fmt.Errorf("too many pods failed (%d failed): %v", len(failedPods), failedPods)
		}

		attempt++

		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for pods status %s: %d/%d ready after %d attempts (failed: %d, pending: %d)",
				status, readyCount, expectedCount, maxAttempts, len(failedPods), len(pendingPods))
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pods status %s: %w", status, ctx.Err())
		case <-time.After(interval):
		}
	}
}
