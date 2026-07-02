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

package dvp

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const (
	bootstrapSecretNamespace = "d8-cloud-instance-manager"
	masterBootstrapSecret    = "manual-bootstrap-for-master"
	workerBootstrapSecret    = "manual-bootstrap-for-worker"
	bootstrapScriptKey       = "bootstrap.sh"
	workerNodeGroupName      = "worker"

	deckhouseNamespace      = "d8-system"
	deckhouseDeploymentName = "deckhouse"

	installPollInterval = 5 * time.Second

	existingInstallReadyTimeout = 10 * time.Minute
)

var controlPlaneNodeLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/master",
}

func waitBootstrapSecrets(ctx context.Context, kube *rest.Config, timeout time.Duration) error {
	cs, err := kubernetes.NewClientsetWithRetry(ctx, kube)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return waitBootstrapSecretsClient(ctx, cs, timeout)
}

func waitBootstrapSecretsClient(ctx context.Context, cs k8s.Interface, timeout time.Duration) error {
	present := func() (bool, error) {
		for _, name := range []string{masterBootstrapSecret, workerBootstrapSecret} {
			_, err := cs.CoreV1().Secrets(bootstrapSecretNamespace).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("get secret %s/%s: %w", bootstrapSecretNamespace, name, err)
			}
		}
		return true, nil
	}
	return pollUntil(ctx, timeout, "bootstrap secrets", present)
}

func waitExistingInstallReady(ctx context.Context, kube *rest.Config, timeout time.Duration) error {
	cs, err := kubernetes.NewClientsetWithRetry(ctx, kube)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return waitExistingInstallReadyClient(ctx, cs, timeout)
}

func waitExistingInstallReadyClient(ctx context.Context, cs k8s.Interface, timeout time.Duration) error {
	check := func() (bool, error) {
		if err := checkHealthClient(ctx, cs); err != nil {
			return false, nil
		}
		for _, name := range []string{masterBootstrapSecret, workerBootstrapSecret} {
			if _, err := cs.CoreV1().Secrets(bootstrapSecretNamespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
				return false, nil
			}
		}
		return true, nil
	}
	return pollUntil(ctx, timeout, "existing Deckhouse installation to become healthy", check)
}

func waitNodesReady(ctx context.Context, kube *rest.Config, def *config.ClusterDefinition, timeout time.Duration) error {
	cs, err := kubernetes.NewClientsetWithRetry(ctx, kube)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return waitNodesReadyClient(ctx, cs, def, timeout)
}

func waitNodesReadyClient(ctx context.Context, cs k8s.Interface, def *config.ClusterDefinition, timeout time.Duration) error {
	want := len(def.Masters) + len(def.Workers)
	if want == 0 {
		return fmt.Errorf("cluster definition declares no nodes")
	}

	ready := func() (bool, error) {
		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("list nodes: %w", err)
		}
		count := 0
		for i := range nodes.Items {
			if isNodeReady(&nodes.Items[i]) {
				count++
			}
		}
		return count >= want, nil
	}
	return pollUntil(ctx, timeout, fmt.Sprintf("%d nodes ready", want), ready)
}

func checkHealth(ctx context.Context, kube *rest.Config) error {
	cs, err := kubernetes.NewClientsetWithRetry(ctx, kube)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return checkHealthClient(ctx, cs)
}

func checkHealthClient(ctx context.Context, cs k8s.Interface) error {
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

func pollUntil(ctx context.Context, timeout time.Duration, what string, check func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ok, err := check()
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	ticker := time.NewTicker(installPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s within %s: %w", what, timeout, ctx.Err())
		case <-ticker.C:
			ok, err := check()
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
	}
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
