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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

const nodeLabelPollInterval = 10 * time.Second

// GetAllNodeNames returns the names of all nodes in the cluster.
func GetAllNodeNames(ctx context.Context, kubeconfig *rest.Config) ([]string, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	names := make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		names = append(names, node.Name)
	}

	logger.Debug("Found %d nodes", len(names))
	return names, nil
}

// GetWorkerNodeNames returns the names of all worker nodes in the cluster.
// A worker node is any node that does NOT have the "node-role.kubernetes.io/control-plane" label.
func GetWorkerNodeNames(ctx context.Context, kubeconfig *rest.Config) ([]string, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	var workers []string
	for _, node := range nodes.Items {
		if _, isMaster := node.Labels["node-role.kubernetes.io/control-plane"]; isMaster {
			continue
		}
		if _, isMaster := node.Labels["node-role.kubernetes.io/master"]; isMaster {
			continue
		}
		workers = append(workers, node.Name)
	}

	logger.Debug("Found %d worker nodes", len(workers))
	return workers, nil
}

// LabelNodes adds a label to each of the specified nodes.
// If a node already has the label with the desired value, it is skipped.
// Uses retry with re-fetch to handle optimistic concurrency conflicts.
func LabelNodes(ctx context.Context, kubeconfig *rest.Config, nodeNames []string, labelKey, labelValue string) error {
	if len(nodeNames) == 0 {
		return nil
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	const maxRetries = 5

	for _, name := range nodeNames {
		var lastErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			node, err := clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get node %s: %w", name, err)
			}

			if node.Labels != nil {
				if v, ok := node.Labels[labelKey]; ok && v == labelValue {
					logger.Debug("Node %s already has label %s=%s", name, labelKey, labelValue)
					lastErr = nil
					break
				}
			}

			if node.Labels == nil {
				node.Labels = make(map[string]string)
			}
			node.Labels[labelKey] = labelValue

			_, lastErr = clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
			if lastErr == nil {
				logger.Info("Labeled node %s with %s=%s", name, labelKey, labelValue)
				break
			}

			if apierrors.IsConflict(lastErr) {
				logger.Debug("Conflict labeling node %s (attempt %d/%d), retrying...", name, attempt+1, maxRetries)
				continue
			}
			return fmt.Errorf("failed to label node %s: %w", name, lastErr)
		}
		if lastErr != nil {
			return fmt.Errorf("failed to label node %s after %d attempts: %w", name, maxRetries, lastErr)
		}
	}

	return nil
}

// NodeHasUnschedulableTaint checks whether a node has NoSchedule or NoExecute taints
// that would prevent DaemonSet pods from scheduling.
func NodeHasUnschedulableTaint(ctx context.Context, kubeconfig *rest.Config, nodeName string) (bool, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %w", err)
	}

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get node %s: %w", nodeName, err)
	}

	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			logger.Debug("Node %s has taint %s=%s:%s", nodeName, taint.Key, taint.Value, taint.Effect)
			return true, nil
		}
	}
	return false, nil
}

// WaitForNodesLabeled waits for all specified nodes to have the given label with the expected value.
// It polls each node in parallel every 10 seconds until all nodes have the label or the context times out.
// Parameters:
//   - ctx: context with timeout/cancellation
//   - kubeconfig: Kubernetes REST config
//   - nodeNames: list of node names to check
//   - labelKey: the label key to look for (e.g., "storage.deckhouse.io/node-ready-for-iscsi")
//   - labelValue: the expected label value (e.g., "true")
func WaitForNodesLabeled(ctx context.Context, kubeconfig *rest.Config, nodeNames []string, labelKey, labelValue string) error {
	if len(nodeNames) == 0 {
		return nil
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	type result struct {
		nodeName string
		err      error
	}

	results := make(chan result, len(nodeNames))

	// Start a goroutine for each node
	for _, nodeName := range nodeNames {
		go func(name string) {
			logger.Info("Waiting for node %s to have label %s=%s...", name, labelKey, labelValue)

			ticker := time.NewTicker(nodeLabelPollInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					results <- result{nodeName: name, err: fmt.Errorf("timeout waiting for label: %w", ctx.Err())}
					return
				case <-ticker.C:
					node, err := clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
					if err != nil {
						logger.Warn("Error getting node %s: %v. Retrying...", name, err)
						continue
					}

					if node.Labels != nil {
						if value, exists := node.Labels[labelKey]; exists && value == labelValue {
							logger.Success("Node %s has label %s=%s", name, labelKey, labelValue)
							results <- result{nodeName: name, err: nil}
							return
						}
					}

					// logger.Debug("Node %s does not have label %s=%s yet", name, labelKey, labelValue)
				}
			}
		}(nodeName)
	}

	// Collect results from all goroutines
	var errors []error
	for i := 0; i < len(nodeNames); i++ {
		res := <-results
		if res.err != nil {
			errors = append(errors, fmt.Errorf("node %s: %w", res.nodeName, res.err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to wait for nodes to be labeled: %v", errors)
	}

	return nil
}
