/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package e2e

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	deckhouseNamespace      = "d8-system"
	deckhouseDeploymentName = "deckhouse"
)

var controlPlaneNodeLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/master",
}

// waitClusterHealthy polls checkClusterHealth until the cluster is healthy or
// the timeout expires. A one-shot check is not enough right after bootstrap:
// Deckhouse converges modules and rolls itself over, leaving short windows
// where the deployment is not Available.
func waitClusterHealthy(ctx context.Context, cs kubernetes.Interface, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	if lastErr = checkClusterHealth(ctx, cs); lastErr == nil {
		return nil
	}

	ticker := time.NewTicker(healthCheckPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for cluster to become healthy within %s: %w (last check: %w)", timeout, ctx.Err(), lastErr)
		case <-ticker.C:
			if lastErr = checkClusterHealth(ctx, cs); lastErr == nil {
				return nil
			}
		}
	}
}

// checkClusterHealth requires all control-plane nodes Ready and the Deckhouse
// deployment Available — the same gate the DVP bootstrap uses.
func checkClusterHealth(ctx context.Context, cs kubernetes.Interface) error {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	controlPlane := 0
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if !isControlPlaneNode(node) {
			continue
		}
		controlPlane++
		if !isNodeReady(node) {
			return fmt.Errorf("control-plane node %s is not Ready", node.Name)
		}
	}
	if controlPlane == 0 {
		return fmt.Errorf("no control-plane nodes found")
	}

	dep, err := cs.AppsV1().Deployments(deckhouseNamespace).Get(ctx, deckhouseDeploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get %s/%s deployment: %w", deckhouseNamespace, deckhouseDeploymentName, err)
	}
	if !isDeploymentAvailable(dep) {
		return fmt.Errorf("deckhouse deployment is not Available")
	}
	return nil
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isControlPlaneNode(node *corev1.Node) bool {
	for _, label := range controlPlaneNodeLabels {
		if _, ok := node.Labels[label]; ok {
			return true
		}
	}
	return false
}

func isDeploymentAvailable(dep *appsv1.Deployment) bool {
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
