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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

const nodeLabelPollInterval = 10 * time.Second

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
