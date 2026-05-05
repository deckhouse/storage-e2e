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
	"strings"
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

	// The operator pod is itself a Ceph admin client: it talks to mons
	// to update CephCluster.status, evaluate CephFilesystem health,
	// etc. Its in-pod ceph.conf was rendered at startup, so until it
	// restarts it keeps using the old `ms_crc_data` value and can't
	// connect to the freshly-bounced mons. Symptom: cephcluster CR
	// flips to phase=Ready/state=Error with `failed to get status. .
	// timed out` until the next reconcile after operator pod recycle.
	// Bounce it now so the operator's view of the cluster lines up
	// with reality before we return.
	if err := RestartRookOperator(ctx, kubeconfig, namespace, 5*time.Minute); err != nil {
		return fmt.Errorf("restart rook-ceph-operator: %w", err)
	}

	// Final sanity check: any CephFilesystem in the namespace must be
	// Ready before we consider the flip "live". This is the gate that
	// catches the MDS-stuck-on-old-CRC class of bug — if the MDS
	// daemons we just bounced fail to rejoin the mons, the CR will
	// linger in a non-Ready phase and we'd rather surface that here
	// than have a downstream csi-cephfs PVC hang for minutes.
	if err := waitCephFilesystemsReady(ctx, kubeconfig, namespace, 5*time.Minute); err != nil {
		return fmt.Errorf("wait CephFilesystem ready after CRC flip: %w", err)
	}

	logger.Success("Server-side ms_crc_data=%s is now live on all Ceph daemons", msCrcDataString(enabled))
	return nil
}

// RestartRookOperator rollout-restarts the rook-operator Deployment
// in the given namespace and waits for the new pod to become Ready.
//
// The operator runs as a Ceph admin client (uses the cluster admin
// keyring + a baked-in ceph.conf to query mon/osd state). When tests
// flip a global wire-protocol knob like `ms_crc_data` and bounce the
// daemons, the operator's existing connections become invalid — but
// without a pod restart it'll keep retrying with the stale ceph.conf
// and the cephcluster CR ends up reporting `HEALTH_ERR` /
// `state: Error` until the next operator reconcile cycle.
//
// Deckhouse packages the rook-operator binary inside a Deployment
// named after the Helm release, which conventionally equals the
// namespace minus the leading `d8-` prefix (`d8-sds-elastic` →
// `sds-elastic`, `d8-sds-replicated-volume` → `sds-replicated-volume`,
// etc.). storage-e2e targets that flavor exclusively — vanilla Rook
// (`rook-ceph-operator` Deployment in `rook-ceph` namespace) is not
// supported here.
func RestartRookOperator(ctx context.Context, kubeconfig *rest.Config, namespace string, timeout time.Duration) error {
	if namespace == "" {
		namespace = kubernetes.DefaultRookNamespace
	}
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	operatorName, ok := strings.CutPrefix(namespace, "d8-")
	if !ok || operatorName == "" {
		return fmt.Errorf("namespace %q is not a deckhouse module namespace (expected d8-<module> prefix); cannot derive rook-operator Deployment name", namespace)
	}
	if _, err := clientset.AppsV1().Deployments(namespace).Get(ctx, operatorName, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("get rook-operator Deployment %s/%s: %w", namespace, operatorName, err)
	}

	logger.Info("Rolling-restarting %s/%s so its Ceph admin client picks up the new ceph.conf", namespace, operatorName)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"storage-e2e/restarted-at":%q}}}}}`, stamp))
	if _, err := clientset.AppsV1().Deployments(namespace).Patch(
		ctx, operatorName, types.StrategicMergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("annotate Deployment %s/%s for rollout: %w", namespace, operatorName, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		d, err := clientset.AppsV1().Deployments(namespace).Get(waitCtx, operatorName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get Deployment %s/%s: %w", namespace, operatorName, err)
		}
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		if d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas >= desired && d.Status.AvailableReplicas >= desired {
			logger.Success("%s/%s is Ready after rollout", namespace, operatorName)
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out after %s waiting for Deployment %s/%s to become ready", timeout, namespace, operatorName)
		case <-ticker.C:
		}
	}
}

// waitCephFilesystemsReady lists every CephFilesystem CR in
// `namespace` and waits for each to reach `status.phase=Ready` (or a
// matching Ready condition). If the namespace has no CephFilesystem
// CRs (RBD-only cluster), the function is a no-op.
func waitCephFilesystemsReady(ctx context.Context, kubeconfig *rest.Config, namespace string, timeout time.Duration) error {
	if namespace == "" {
		namespace = kubernetes.DefaultRookNamespace
	}
	dynamicClient, err := kubernetes.NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	list, err := dynamicClient.Resource(kubernetes.CephFilesystemGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list CephFilesystem in %s: %w", namespace, err)
	}
	if len(list.Items) == 0 {
		return nil
	}

	for i := range list.Items {
		name := list.Items[i].GetName()
		if err := kubernetes.WaitForCephFilesystemReady(ctx, kubeconfig, namespace, name, timeout); err != nil {
			return fmt.Errorf("CephFilesystem %s/%s did not become Ready after CRC flip: %w", namespace, name, err)
		}
	}
	return nil
}

// RestartCephDaemons rollout-restarts every Rook-managed Ceph daemon
// Deployment that consumes `/etc/ceph/ceph.conf` (mon, mgr, osd, mds,
// rgw) and waits for each to reach its desired Ready replica count.
//
// Why all five roles, not just mon/mgr/osd: a global ConfigMap knob
// like `ms_crc_data` lives in ceph.conf, which means every daemon
// needs to be restarted for it to take effect. If only mon/mgr/osd
// are bounced and an MDS keeps running with the old value, the
// resulting CRC mismatch silently severs the MDS↔mon messenger
// channel, CephFS goes degraded, and any csi-cephfs PVC hangs in
// Pending until somebody (often the human running the test) bounces
// MDS by hand. Including `rook-ceph-mds` here is what unblocks the
// CephFS half of the msCrcData matrix.
//
// The selector also covers `rook-ceph-rgw` for forward-compat with
// future S3 tests; if no rgw Deployments exist in the cluster, the
// match list is just smaller and the function continues. Operator
// restart is intentionally out of scope here — see RestartRookOperator.
func RestartCephDaemons(ctx context.Context, kubeconfig *rest.Config, namespace string, timeout time.Duration) error {
	if namespace == "" {
		namespace = kubernetes.DefaultRookNamespace
	}
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Rook labels each Ceph daemon Deployment with `app=rook-ceph-<role>`.
	labelSel := "app in (rook-ceph-mon,rook-ceph-mgr,rook-ceph-osd,rook-ceph-mds,rook-ceph-rgw)"
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
