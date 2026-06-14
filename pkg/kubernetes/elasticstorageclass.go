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

// ElasticStorageClassGVR is the GroupVersionResource of the sds-elastic
// ElasticStorageClass CR (cluster-scoped). The controller maps it to a Ceph
// pool/filesystem plus a 1:1-named csi-ceph CephStorageClass and a core
// storage.k8s.io/v1 StorageClass of the same name.
var ElasticStorageClassGVR = schema.GroupVersionResource{
	Group:    "storage.deckhouse.io",
	Version:  "v1alpha1",
	Resource: "elasticstorageclasses",
}

// ElasticStorageClass spec enums (mirrored from sds-elastic/api/v1alpha1 so
// storage-e2e stays free of a build dependency on the module). Keep in sync.
const (
	ElasticStorageClassTypeRBD    = "RBD"
	ElasticStorageClassTypeCephFS = "CephFS"

	ElasticReplicationAvailabilityWithoutConsistency = "AvailabilityWithoutConsistency"
	ElasticReplicationConsistencyAndAvailability     = "ConsistencyAndAvailability"
	ElasticReplicationHighRedundancy                 = "HighRedundancy"
	ElasticReplicationErasureCodedCompact            = "ErasureCodedCompact"
)

// Well-known ElasticStorageClass status condition types (mirror of
// ESCCondition* in the api package).
const (
	ElasticStorageClassConditionReady                = "Ready"
	ElasticStorageClassConditionPoolReady            = "PoolReady"
	ElasticStorageClassConditionCsiStorageClassReady = "CsiStorageClassReady"
)

// Well-known ElasticStorageClass teardown reasons set on the aggregate Ready
// condition while the CR is being deleted (mirror of ESCReason* in the api
// package).
const (
	ElasticStorageClassReasonBoundVolumesExist  = "BoundVolumesExist"
	ElasticStorageClassReasonDataPresentInPool  = "DataPresentInPool"
	ElasticStorageClassReasonFilesystemNotEmpty = "FilesystemNotEmpty"
	ElasticStorageClassReasonTerminating        = "Terminating"
)

// ElasticStorageClassForceDeleteAnnotation, set to "true" on an
// ElasticStorageClass, authorises the destructive purge of a non-empty RBD
// pool (the controller propagates it to the underlying CephBlockPool as the
// Rook force-deletion annotation). It NEVER bypasses the bound-PV guard.
// Mirror of v1alpha1 ESCForceDeleteAnnotation.
const ElasticStorageClassForceDeleteAnnotation = "sds-elastic.deckhouse.io/force-deletion"

// ElasticStorageClassParams is the minimal description of an
// ElasticStorageClass the e2e suite renders.
type ElasticStorageClassParams struct {
	// Name of the ElasticStorageClass; also the name of the resulting
	// csi-ceph CephStorageClass and the core k8s StorageClass.
	Name string

	// ClusterRef is the ElasticCluster this ESC belongs to. Required.
	ClusterRef string

	// Type selects RBD (block) or CephFS (shared filesystem). Required.
	Type string

	// Replication picks the high-level replication strategy. Empty defaults
	// to ConsistencyAndAvailability (the CRD default).
	Replication string

	// Labels / Annotations are applied verbatim to metadata.
	Labels      map[string]string
	Annotations map[string]string
}

func buildElasticStorageClassObject(params ElasticStorageClassParams) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"clusterRef": params.ClusterRef,
		"type":       params.Type,
	}
	if params.Replication != "" {
		spec["replication"] = params.Replication
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
			"apiVersion": ElasticStorageClassGVR.Group + "/" + ElasticStorageClassGVR.Version,
			"kind":       "ElasticStorageClass",
			"metadata":   meta,
			"spec":       spec,
		},
	}
}

// CreateElasticStorageClass creates (or updates the spec of) an
// ElasticStorageClass. Idempotent; fails fast on a Terminating existing CR.
func CreateElasticStorageClass(ctx context.Context, kubeconfig *rest.Config, params ElasticStorageClassParams) error {
	if params.Name == "" {
		return fmt.Errorf("ElasticStorageClass requires a Name")
	}
	if params.ClusterRef == "" {
		return fmt.Errorf("ElasticStorageClass %s requires a ClusterRef", params.Name)
	}
	if params.Type == "" {
		return fmt.Errorf("ElasticStorageClass %s requires a Type (RBD or CephFS)", params.Name)
	}

	obj := buildElasticStorageClassObject(params)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating ElasticStorageClass %s (clusterRef=%s, type=%s, replication=%s)",
		params.Name, params.ClusterRef, params.Type, params.Replication)

	_, err = dynamicClient.Resource(ElasticStorageClassGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		logger.Success("ElasticStorageClass %s created", params.Name)
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ElasticStorageClass %s: %w", params.Name, err)
	}

	logger.Info("ElasticStorageClass %s already exists, updating spec", params.Name)
	existing, err := dynamicClient.Resource(ElasticStorageClassGVR).Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing ElasticStorageClass %s: %w", params.Name, err)
	}
	if err := errIfTerminating(existing, "ElasticStorageClass", params.Name); err != nil {
		return err
	}
	existing.Object["spec"] = obj.Object["spec"]
	if _, err := dynamicClient.Resource(ElasticStorageClassGVR).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ElasticStorageClass %s: %w", params.Name, err)
	}
	return nil
}

// WaitForElasticStorageClassCondition blocks until the named ESC has a status
// condition of the given type at the wanted status. Refuses to wait on a
// Terminating object (see WaitForElasticClusterCondition).
func WaitForElasticStorageClassCondition(ctx context.Context, kubeconfig *rest.Config, name, condType, wantStatus string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, ElasticStorageClassGVR, "", name,
		timeout, PollTickInterval, "ElasticStorageClass",
		func(obj *unstructured.Unstructured) (bool, string) {
			status, reason, message, found := findUnstructuredCondition(obj, condType)
			if found && status == wantStatus {
				return true, fmt.Sprintf("%s=%s reason=%s", condType, status, reason)
			}
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			logger.Debug("ElasticStorageClass %s %s=%q (want %q) phase=%q reason=%q msg=%q",
				name, condType, status, wantStatus, phase, reason, message)
			return false, ""
		},
	)
}

// WaitForElasticStorageClassReady waits for the aggregate Ready condition to
// flip to True (pool/filesystem provisioned, csi-ceph SC materialised).
func WaitForElasticStorageClassReady(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	return WaitForElasticStorageClassCondition(ctx, kubeconfig, name, ElasticStorageClassConditionReady, "True", timeout)
}

// GetElasticStorageClassCondition returns the (status, reason, message) of the
// named condition on the ESC, plus whether it exists. Single GET; wrap in a
// Gomega Eventually/Consistently to assert teardown-guard reasons.
func GetElasticStorageClassCondition(ctx context.Context, kubeconfig *rest.Config, name, condType string) (status, reason, message string, found bool, err error) {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	obj, err := dynamicClient.Resource(ElasticStorageClassGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to get ElasticStorageClass %s: %w", name, err)
	}
	status, reason, message, found = findUnstructuredCondition(obj, condType)
	return status, reason, message, found, nil
}

// AnnotateElasticStorageClassForceDeletion sets the force-deletion annotation
// on the named ESC, authorising the destructive purge of a non-empty RBD
// pool. It never bypasses the bound-PV guard. Idempotent; retries on
// optimistic-concurrency conflicts.
func AnnotateElasticStorageClassForceDeletion(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	const maxRetries = 5
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		existing, err := dynamicClient.Resource(ElasticStorageClassGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ElasticStorageClass %s: %w", name, err)
		}
		annotations := existing.GetAnnotations()
		if annotations[ElasticStorageClassForceDeleteAnnotation] == "true" {
			logger.Debug("ElasticStorageClass %s already has force-deletion annotation", name)
			return nil
		}
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[ElasticStorageClassForceDeleteAnnotation] = "true"
		existing.SetAnnotations(annotations)

		_, lastErr = dynamicClient.Resource(ElasticStorageClassGVR).Update(ctx, existing, metav1.UpdateOptions{})
		if lastErr == nil {
			logger.Info("Annotated ElasticStorageClass %s with %s=true", name, ElasticStorageClassForceDeleteAnnotation)
			return nil
		}
		if apierrors.IsConflict(lastErr) {
			logger.Debug("Conflict annotating ElasticStorageClass %s (attempt %d/%d), retrying...", name, attempt+1, maxRetries)
			continue
		}
		return fmt.Errorf("failed to annotate ElasticStorageClass %s: %w", name, lastErr)
	}
	return fmt.Errorf("failed to annotate ElasticStorageClass %s after %d attempts: %w", name, maxRetries, lastErr)
}

// DeleteElasticStorageClass removes an ElasticStorageClass. Idempotent.
// Fire-and-forget: the controller runs the destructive pool/filesystem
// teardown (and the bound-PV guard) under its finalizer. Follow with
// WaitForElasticStorageClassGone.
func DeleteElasticStorageClass(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	if err := dynamicClient.Resource(ElasticStorageClassGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ElasticStorageClass %s: %w", name, err)
	}
	logger.Info("Deleted ElasticStorageClass %s (controller teardown in progress)", name)
	return nil
}

// ElasticStorageClassGoneTimeout is the default budget for
// WaitForElasticStorageClassGone. Pool/filesystem teardown plus the csi-ceph
// SC removal take a few minutes; a force-deletion purge of a populated pool
// can take longer.
const ElasticStorageClassGoneTimeout = 10 * time.Minute

// WaitForElasticStorageClassGone polls until the ESC GET returns NotFound.
func WaitForElasticStorageClassGone(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = ElasticStorageClassGoneTimeout
	}
	return pollResourceUntilGone(
		ctx, kubeconfig, ElasticStorageClassGVR, "", name,
		timeout, PollTickInterval, "ElasticStorageClass",
	)
}
