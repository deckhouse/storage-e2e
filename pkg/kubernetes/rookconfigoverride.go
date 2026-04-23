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
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// RookConfigOverrideName is the well-known ConfigMap name Rook reads Ceph
// config overrides from (see Rook docs: "Advanced Configuration – Custom
// ceph.conf Settings"). Rook watches this ConfigMap in its operator namespace
// and injects the `config` key into `/etc/ceph/ceph.conf` of every Ceph daemon.
const RookConfigOverrideName = "rook-config-override"

// SetRookConfigOverride creates or updates the `rook-config-override` ConfigMap
// in the given Rook operator namespace so that Ceph daemons pick up the
// provided global settings.
//
// The ConfigMap format expected by Rook is:
//
//	apiVersion: v1
//	kind: ConfigMap
//	metadata:
//	  name: rook-config-override
//	  namespace: <rook-namespace>
//	data:
//	  config: |
//	    [global]
//	    key1 = value1
//	    key2 = value2
//
// `globals` is rendered under `[global]`. Keys are sorted for a stable output.
// Passing an empty/nil `globals` map produces an empty `[global]` section,
// which effectively clears previously-set overrides.
func SetRookConfigOverride(ctx context.Context, kubeconfig *rest.Config, namespace string, globals map[string]string) error {
	if namespace == "" {
		return fmt.Errorf("namespace is required")
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	cfg := renderCephGlobalConfig(globals)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RookConfigOverrideName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"config": cfg,
		},
	}

	existing, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, RookConfigOverrideName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Creating ConfigMap %s/%s with Ceph global overrides (%d keys)", namespace, RookConfigOverrideName, len(globals))
			if _, err := clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create ConfigMap %s/%s: %w", namespace, RookConfigOverrideName, err)
			}
			return nil
		}
		return fmt.Errorf("failed to get ConfigMap %s/%s: %w", namespace, RookConfigOverrideName, err)
	}

	logger.Info("Updating ConfigMap %s/%s with Ceph global overrides (%d keys)", namespace, RookConfigOverrideName, len(globals))
	existing.Data = cm.Data
	if _, err := clientset.CoreV1().ConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ConfigMap %s/%s: %w", namespace, RookConfigOverrideName, err)
	}
	return nil
}

// DeleteRookConfigOverride removes the `rook-config-override` ConfigMap. It
// is safe to call when the ConfigMap does not exist.
func DeleteRookConfigOverride(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace is required")
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	if err := clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, RookConfigOverrideName, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ConfigMap %s/%s: %w", namespace, RookConfigOverrideName, err)
	}
	logger.Info("Deleted ConfigMap %s/%s", namespace, RookConfigOverrideName)
	return nil
}

// renderCephGlobalConfig renders a `[global]` section for ceph.conf from the
// provided key/value pairs. Keys are sorted so the rendered output is stable
// across calls with logically-equivalent maps (avoids unnecessary CM updates).
func renderCephGlobalConfig(globals map[string]string) string {
	var b strings.Builder
	b.WriteString("[global]\n")

	keys := make([]string, 0, len(globals))
	for k := range globals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, globals[k])
	}
	return b.String()
}
