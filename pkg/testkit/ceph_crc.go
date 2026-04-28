/*
Copyright 2026 Flant JSC

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

package testkit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// EnableServerCRC is the readable counterpart of
// `SetMsCrcDataOnServer(..., ptr.To(true))`. It writes
// `ms_crc_data = true` into rook-config-override and rolling-restarts
// mon/mgr/osd so the override is live on every daemon before returning.
//
// Useful for tests that want the Ceph cluster in an explicit CRC-on state
// (the default Ceph behaviour, but pinned in the ConfigMap so the test
// can assert on it).
func EnableServerCRC(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	enabled := true
	return SetMsCrcDataOnServer(ctx, kubeconfig, namespace, &enabled)
}

// DisableServerCRC flips Ceph into the "CRC off" state:
// `ms_crc_data = false` in rook-config-override + rolling-restart of
// mon/mgr/osd. Paired with a csi-ceph client that still defaults to
// `msCrcData=true`, this reproduces the msCrcData matrix mismatch case.
func DisableServerCRC(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	enabled := false
	return SetMsCrcDataOnServer(ctx, kubeconfig, namespace, &enabled)
}

// ResetServerCRCToDefault removes `ms_crc_data` from rook-config-override
// (rendered `[global]` section becomes empty). Ceph falls back to its
// compile-time default (ms_crc_data = true), matching a freshly-installed
// cluster. Convenient for AfterAll / AfterEach restoration.
func ResetServerCRCToDefault(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	return SetMsCrcDataOnServer(ctx, kubeconfig, namespace, nil)
}

// SetMsCrcDataOnServer rewrites `rook-config-override` so that only
// `ms_crc_data = <enabled>` ends up under `[global]` (nil removes the key
// entirely, falling back to Ceph's compile-time default = true).
//
// After flipping the ConfigMap, it force-restarts mon/mgr/osd Deployments
// in the Rook namespace and waits for them to converge. Idempotent: when
// the ConfigMap already encodes the desired state, nothing is restarted.
//
// Prefer EnableServerCRC / DisableServerCRC / ResetServerCRCToDefault at
// call sites for readability; this lower-level primitive exists so a
// boolean test parameter (e.g. a CRC compatibility matrix) doesn't have to branch.
func SetMsCrcDataOnServer(ctx context.Context, kubeconfig *rest.Config, namespace string, enabled *bool) error {
	if namespace == "" {
		namespace = kubernetes.DefaultRookNamespace
	}

	overrides := renderMsCrcDataOverrides(enabled)
	wantConfig := kubernetes.RenderCephGlobalConfig(overrides)

	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	existing, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, kubernetes.RookConfigOverrideName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get %s/%s: %w", namespace, kubernetes.RookConfigOverrideName, err)
	}
	currentConfig := ""
	if existing != nil {
		currentConfig = existing.Data["config"]
	}

	if currentConfig == wantConfig {
		logger.Info("rook-config-override already has ms_crc_data=%s, skipping daemon restart",
			msCrcDataString(enabled))
		return nil
	}

	logger.Info("Setting server-side ms_crc_data=%s in rook-config-override", msCrcDataString(enabled))
	if err := kubernetes.SetRookConfigOverride(ctx, kubeconfig, namespace, overrides); err != nil {
		return fmt.Errorf("set rook-config-override: %w", err)
	}

	// Rook operator notices CM changes on its next reconcile loop; force
	// a rolling restart of the core Ceph daemons so the new
	// `/etc/ceph/ceph.conf` takes effect right now.
	if err := RestartCephDaemons(ctx, kubeconfig, namespace, 10*time.Minute); err != nil {
		return fmt.Errorf("restart ceph daemons: %w", err)
	}
	logger.Success("Server-side ms_crc_data=%s is now live on all Ceph daemons", msCrcDataString(enabled))
	return nil
}

// RestartCephDaemons rollout-restarts Rook's mon/mgr/osd Deployments and
// waits for them to reach their desired ready replica count.
func RestartCephDaemons(ctx context.Context, kubeconfig *rest.Config, namespace string, timeout time.Duration) error {
	if namespace == "" {
		namespace = kubernetes.DefaultRookNamespace
	}
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Rook labels each Ceph daemon Deployment with `app=rook-ceph-<role>`.
	// We restart the daemons that actually consume `/etc/ceph/ceph.conf`:
	// mon, mgr and osd. (The operator itself reads rook-config-override
	// directly and does not need a bounce.)
	labelSel := "app in (rook-ceph-mon,rook-ceph-mgr,rook-ceph-osd)"
	deployList, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSel})
	if err != nil {
		return fmt.Errorf("list ceph daemon Deployments (%s): %w", labelSel, err)
	}
	if len(deployList.Items) == 0 {
		return fmt.Errorf("no Ceph daemon Deployments matched %q in namespace %s — is Rook running?", labelSel, namespace)
	}

	names := make([]string, 0, len(deployList.Items))
	for i := range deployList.Items {
		names = append(names, deployList.Items[i].Name)
	}
	logger.Info("Rolling-restarting %d Ceph daemon Deployment(s): %v", len(names), names)

	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"storage-e2e/restarted-at":%q}}}}}`, stamp))

	for _, name := range names {
		if _, err := clientset.AppsV1().Deployments(namespace).Patch(
			ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("annotate Deployment %s/%s for rollout: %w", namespace, name, err)
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		ready := 0
		for _, name := range names {
			d, err := clientset.AppsV1().Deployments(namespace).Get(waitCtx, name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get Deployment %s/%s: %w", namespace, name, err)
			}
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			if d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas >= desired && d.Status.AvailableReplicas >= desired {
				ready++
			}
		}
		if ready == len(names) {
			logger.Success("All %d Ceph daemon Deployment(s) report Ready after rollout", len(names))
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out after %s waiting for %d Ceph daemon Deployments to become ready (%d/%d)",
				timeout, len(names), ready, len(names))
		case <-ticker.C:
		}
	}
}

// renderMsCrcDataOverrides turns a *bool into the minimal rook-config-override
// key/value map used by the msCrcData test matrix.
func renderMsCrcDataOverrides(enabled *bool) map[string]string {
	if enabled == nil {
		return nil
	}
	return map[string]string{
		"ms_crc_data": strconv.FormatBool(*enabled),
	}
}

func msCrcDataString(enabled *bool) string {
	if enabled == nil {
		return "<unset>"
	}
	return strconv.FormatBool(*enabled)
}
