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

package kubernetes

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// ElasticClusterGVR is the GroupVersionResource of the sds-elastic
// ElasticCluster CR. It is cluster-scoped (no namespace), so all dynamic
// calls below omit .Namespace(). The CR is the high-level entry point of the
// sds-elastic module: the controller turns it into a Rook CephCluster
// (renamed group internal.sdselastic.deckhouse.io) backed by LVM-local OSDs.
var ElasticClusterGVR = schema.GroupVersionResource{
	Group:    "storage.deckhouse.io",
	Version:  "v1alpha1",
	Resource: "elasticclusters",
}

// Well-known ElasticCluster status condition types. Mirrored from
// sds-elastic/api/v1alpha1 (ECCondition*) and kept here as plain strings so
// storage-e2e does not take a build dependency on the sds-elastic module.
// Keep in sync with the api package.
const (
	ElasticClusterConditionReady            = "Ready"
	ElasticClusterConditionStorageReady     = "StorageReady"
	ElasticClusterConditionCephClusterReady = "CephClusterReady"
	ElasticClusterConditionCredentialsReady = "CredentialsReady"
	ElasticClusterConditionCsiCephReady     = "CsiCephReady"
)

// Well-known ElasticCluster teardown reasons set on the aggregate Ready
// condition while the CR is being deleted. Domain-level on purpose (they
// never name the underlying Rook/csi-ceph resources). Mirrored from
// sds-elastic/api/v1alpha1 (ECReason*).
const (
	ElasticClusterReasonStorageClassesExist = "StorageClassesExist"
	ElasticClusterReasonVolumesExist        = "VolumesExist"
	ElasticClusterReasonTerminating         = "Terminating"
)

// ElasticClusterParams is the minimal description of an ElasticCluster the
// e2e suite needs to render. Selectors are expressed as plain matchLabels
// maps (the only selector form the suite exercises); spec.network is emitted
// only when both CIDRs are provided (otherwise Rook uses host networking on
// every storage-node IP, which is what the default e2e cluster wants).
type ElasticClusterParams struct {
	// Name of the ElasticCluster (cluster-scoped, so no namespace).
	Name string

	// NodeSelectorMatchLabels populates spec.storage.nodeSelector.matchLabels.
	// Must be non-empty: it is how the controller picks storage nodes.
	NodeSelectorMatchLabels map[string]string

	// BlockDeviceSelectorMatchLabels populates
	// spec.storage.blockDeviceSelector.matchLabels. Must be non-empty: it is
	// how the controller adopts BlockDevices for OSDs.
	BlockDeviceSelectorMatchLabels map[string]string

	// NetworkPublic / NetworkCluster optionally pin spec.network.{public,
	// cluster}. Both must be set together; otherwise spec.network is omitted.
	NetworkPublic  string
	NetworkCluster string

	// Labels / Annotations are applied verbatim to metadata.
	Labels      map[string]string
	Annotations map[string]string
}

// buildElasticClusterObject renders the unstructured ElasticCluster object
// from params. It deliberately sets only the fields the e2e suite controls;
// everything else is left to the CRD defaults / controller.
func buildElasticClusterObject(params ElasticClusterParams) *unstructured.Unstructured {
	storage := map[string]interface{}{
		"nodeSelector": map[string]interface{}{
			"matchLabels": toStringMapInterface(params.NodeSelectorMatchLabels),
		},
		"blockDeviceSelector": map[string]interface{}{
			"matchLabels": toStringMapInterface(params.BlockDeviceSelectorMatchLabels),
		},
	}

	spec := map[string]interface{}{
		"storage": storage,
	}
	if params.NetworkPublic != "" && params.NetworkCluster != "" {
		spec["network"] = map[string]interface{}{
			"public":  params.NetworkPublic,
			"cluster": params.NetworkCluster,
		}
	}

	meta := map[string]interface{}{
		"name": params.Name,
	}
	if len(params.Labels) > 0 {
		meta["labels"] = toStringMapInterface(params.Labels)
	}
	if len(params.Annotations) > 0 {
		meta["annotations"] = toStringMapInterface(params.Annotations)
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": ElasticClusterGVR.Group + "/" + ElasticClusterGVR.Version,
			"kind":       "ElasticCluster",
			"metadata":   meta,
			"spec":       spec,
		},
	}
}

// CreateElasticCluster creates (or updates the spec of) an ElasticCluster.
// Idempotent: re-running overwrites spec so callers can tweak ElasticClusterParams
// and re-apply. Fails fast if the existing CR is Terminating (its spec update
// would be a no-op while the finalizer unwinds, and a follow-up wait-Ready
// would hang on a never-Ready object).
func CreateElasticCluster(ctx context.Context, kubeconfig *rest.Config, params ElasticClusterParams) error {
	if params.Name == "" {
		return fmt.Errorf("ElasticCluster requires a Name")
	}
	if len(params.NodeSelectorMatchLabels) == 0 {
		return fmt.Errorf("ElasticCluster %s requires NodeSelectorMatchLabels", params.Name)
	}
	if len(params.BlockDeviceSelectorMatchLabels) == 0 {
		return fmt.Errorf("ElasticCluster %s requires BlockDeviceSelectorMatchLabels", params.Name)
	}

	obj := buildElasticClusterObject(params)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating ElasticCluster %s (nodeSelector=%v, blockDeviceSelector=%v)",
		params.Name, params.NodeSelectorMatchLabels, params.BlockDeviceSelectorMatchLabels)

	_, err = dynamicClient.Resource(ElasticClusterGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		logger.Success("ElasticCluster %s created", params.Name)
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ElasticCluster %s: %w", params.Name, err)
	}

	logger.Info("ElasticCluster %s already exists, updating spec", params.Name)
	existing, err := dynamicClient.Resource(ElasticClusterGVR).Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing ElasticCluster %s: %w", params.Name, err)
	}
	if err := errIfTerminating(existing, "ElasticCluster", params.Name); err != nil {
		return err
	}
	existing.Object["spec"] = obj.Object["spec"]
	if _, err := dynamicClient.Resource(ElasticClusterGVR).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ElasticCluster %s: %w", params.Name, err)
	}
	return nil
}

// WaitForElasticClusterCondition blocks until the named ElasticCluster has a
// status condition of the given type observed at the wanted status (e.g.
// type="Ready", status="True"). It refuses to wait on a Terminating object —
// use GetElasticClusterCondition + a Gomega Eventually loop when you need to
// observe a teardown-guard reason on a CR that is already being deleted.
func WaitForElasticClusterCondition(ctx context.Context, kubeconfig *rest.Config, name, condType, wantStatus string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, ElasticClusterGVR, "", name,
		timeout, PollTickInterval, "ElasticCluster",
		func(obj *unstructured.Unstructured) (bool, string) {
			status, reason, message, found := findUnstructuredCondition(obj, condType)
			if found && status == wantStatus {
				return true, fmt.Sprintf("%s=%s reason=%s", condType, status, reason)
			}
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			logger.Debug("ElasticCluster %s %s=%q (want %q) phase=%q reason=%q msg=%q",
				name, condType, status, wantStatus, phase, reason, message)
			return false, ""
		},
	)
}

// WaitForElasticClusterReady waits for the aggregate Ready condition to flip
// to True (i.e. storage staged, Rook CephCluster up, credentials backed up,
// csi-ceph wired).
func WaitForElasticClusterReady(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	return WaitForElasticClusterCondition(ctx, kubeconfig, name, ElasticClusterConditionReady, "True", timeout)
}

// GetElasticClusterCondition returns the (status, reason, message) of the
// named condition on the ElasticCluster, plus whether the condition exists.
// Single GET, no waiting — meant to be wrapped in a Gomega Eventually /
// Consistently when asserting teardown-guard reasons on a Terminating CR.
func GetElasticClusterCondition(ctx context.Context, kubeconfig *rest.Config, name, condType string) (status, reason, message string, found bool, err error) {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	obj, err := dynamicClient.Resource(ElasticClusterGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to get ElasticCluster %s: %w", name, err)
	}
	status, reason, message, found = findUnstructuredCondition(obj, condType)
	return status, reason, message, found, nil
}

// ElasticClusterCephTopology mirrors status.cephTopology — the effective
// mon/mgr counts the controller asked Rook to apply, plus the audit reason.
type ElasticClusterCephTopology struct {
	MonCount int64
	MgrCount int64
	Reason   string
}

// GetElasticClusterCephTopology reads status.cephTopology of the named
// ElasticCluster. found is false when the controller has not recorded a
// topology yet (cluster still bootstrapping).
func GetElasticClusterCephTopology(ctx context.Context, kubeconfig *rest.Config, name string) (topology ElasticClusterCephTopology, found bool, err error) {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return ElasticClusterCephTopology{}, false, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	obj, err := dynamicClient.Resource(ElasticClusterGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return ElasticClusterCephTopology{}, false, fmt.Errorf("failed to get ElasticCluster %s: %w", name, err)
	}
	raw, ok, err := unstructured.NestedMap(obj.Object, "status", "cephTopology")
	if err != nil {
		return ElasticClusterCephTopology{}, false, fmt.Errorf("read status.cephTopology of ElasticCluster %s: %w", name, err)
	}
	if !ok || raw == nil {
		return ElasticClusterCephTopology{}, false, nil
	}
	mon, _, _ := unstructured.NestedInt64(obj.Object, "status", "cephTopology", "monCount")
	mgr, _, _ := unstructured.NestedInt64(obj.Object, "status", "cephTopology", "mgrCount")
	reason, _, _ := unstructured.NestedString(obj.Object, "status", "cephTopology", "reason")
	return ElasticClusterCephTopology{MonCount: mon, MgrCount: mgr, Reason: reason}, true, nil
}

// DeleteElasticCluster removes an ElasticCluster. Idempotent (NotFound is
// swallowed). Fire-and-forget: the controller then runs its ordered teardown
// finalizer (delete CephCluster + csi-ceph wiring the operator cannot delete
// by hand). Follow with WaitForElasticClusterGone to be sure it is GC'd.
func DeleteElasticCluster(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	if err := dynamicClient.Resource(ElasticClusterGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ElasticCluster %s: %w", name, err)
	}
	logger.Info("Deleted ElasticCluster %s (controller teardown in progress)", name)
	return nil
}

// ElasticClusterGoneTimeout is the default budget for WaitForElasticClusterGone.
// The controller tears down the whole Rook CephCluster (mon/mgr/osd drain,
// CRUSH map removal) before releasing the finalizer — easily 10+ minutes.
const ElasticClusterGoneTimeout = 15 * time.Minute

// WaitForElasticClusterGone polls until the ElasticCluster GET returns
// NotFound (Kubernetes has GC'd it after the controller finalizer completed).
func WaitForElasticClusterGone(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = ElasticClusterGoneTimeout
	}
	return pollResourceUntilGone(
		ctx, kubeconfig, ElasticClusterGVR, "", name,
		timeout, PollTickInterval, "ElasticCluster",
	)
}

// findUnstructuredCondition extracts the (status, reason, message) of a
// metav1.Condition-shaped entry from obj.status.conditions. Shared by the
// ElasticCluster and ElasticStorageClass helpers in this package.
func findUnstructuredCondition(obj *unstructured.Unstructured, condType string) (status, reason, message string, found bool) {
	conditions, ok, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !ok {
		return "", "", "", false
	}
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		if t != condType {
			continue
		}
		status, _, _ = unstructured.NestedString(m, "status")
		reason, _, _ = unstructured.NestedString(m, "reason")
		message, _, _ = unstructured.NestedString(m, "message")
		return status, reason, message, true
	}
	return "", "", "", false
}

// toStringMapInterface converts a map[string]string to map[string]interface{}
// so it can be embedded into an unstructured object tree.
func toStringMapInterface(in map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
