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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// CephBlockPoolGVR is the GroupVersionResource of Rook's CephBlockPool.
var CephBlockPoolGVR = schema.GroupVersionResource{
	Group:    "ceph.rook.io",
	Version:  "v1",
	Resource: "cephblockpools",
}

// CephBlockPoolConfig describes a minimal replicated or erasure-coded Ceph
// RBD pool managed by Rook. Exactly one of ReplicaSize or ErasureCoded must
// be set; leaving both zero defaults to a single-replica pool suitable for
// single-node test clusters.
type CephBlockPoolConfig struct {
	// Name of the CephBlockPool CR (also becomes the Ceph pool name).
	Name string

	// Namespace the Rook operator watches (typically "d8-sds-elastic").
	Namespace string

	// FailureDomain is the CRUSH failure domain: "host" or "osd" (default: "host").
	FailureDomain string

	// --- Replicated pool knobs (used when ErasureCoded is nil) ---

	// ReplicaSize is the number of object copies. Default: 1.
	ReplicaSize int

	// RequireSafeReplicaSize toggles Ceph's safeguard against single-replica
	// pools. When nil, it is set to `false` for ReplicaSize==1 (unsafe single
	// replica, accepted for e2e test clusters) and left unset otherwise.
	RequireSafeReplicaSize *bool

	// --- Erasure-coded pool knobs ---

	// ErasureCoded, when non-nil, produces an EC pool instead of a replicated
	// one. Its fields map to `spec.erasureCoded.{dataChunks,codingChunks}`.
	ErasureCoded *CephBlockPoolErasureCoded
}

// CephBlockPoolErasureCoded configures a Ceph erasure-coded RBD pool.
type CephBlockPoolErasureCoded struct {
	DataChunks   int
	CodingChunks int
}

// CreateCephBlockPool creates (or updates, if already present) a CephBlockPool
// in the given namespace from the provided configuration. It is idempotent and
// safe to call on every test run.
func CreateCephBlockPool(ctx context.Context, kubeconfig *rest.Config, cfg CephBlockPoolConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("CephBlockPool name is required")
	}
	if cfg.Namespace == "" {
		return fmt.Errorf("CephBlockPool namespace is required")
	}
	if cfg.ErasureCoded == nil && cfg.ReplicaSize <= 0 {
		cfg.ReplicaSize = 1
	}
	if cfg.FailureDomain == "" {
		cfg.FailureDomain = "host"
	}

	spec := map[string]interface{}{
		"failureDomain": cfg.FailureDomain,
	}

	if cfg.ErasureCoded != nil {
		if cfg.ErasureCoded.DataChunks <= 0 || cfg.ErasureCoded.CodingChunks <= 0 {
			return fmt.Errorf("ErasureCoded pool requires positive dataChunks and codingChunks")
		}
		spec["erasureCoded"] = map[string]interface{}{
			"dataChunks":   int64(cfg.ErasureCoded.DataChunks),
			"codingChunks": int64(cfg.ErasureCoded.CodingChunks),
		}
	} else {
		replicated := map[string]interface{}{
			"size": int64(cfg.ReplicaSize),
		}
		requireSafe := cfg.RequireSafeReplicaSize
		if requireSafe == nil && cfg.ReplicaSize == 1 {
			f := false
			requireSafe = &f
		}
		if requireSafe != nil {
			replicated["requireSafeReplicaSize"] = *requireSafe
		}
		spec["replicated"] = replicated
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "ceph.rook.io/v1",
			"kind":       "CephBlockPool",
			"metadata": map[string]interface{}{
				"name":      cfg.Name,
				"namespace": cfg.Namespace,
			},
			"spec": spec,
		},
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating CephBlockPool %s/%s", cfg.Namespace, cfg.Name)
	_, err = dynamicClient.Resource(CephBlockPoolGVR).Namespace(cfg.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		logger.Success("CephBlockPool %s/%s created", cfg.Namespace, cfg.Name)
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephBlockPool %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}

	logger.Info("CephBlockPool %s/%s already exists, updating spec", cfg.Namespace, cfg.Name)
	existing, err := dynamicClient.Resource(CephBlockPoolGVR).Namespace(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing CephBlockPool %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	existing.Object["spec"] = spec
	if _, err := dynamicClient.Resource(CephBlockPoolGVR).Namespace(cfg.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephBlockPool %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	return nil
}

// WaitForCephBlockPoolReady blocks until the CephBlockPool reports
// `status.phase == "Ready"`. Rook transitions the pool from Progressing to
// Ready once the Ceph OSDs have accepted the new pool and its CRUSH rule.
//
// Per-call deadlines and loud (WARN) logging on consecutive network failures
// are inherited from pollResourceUntilReady, so a dropped SSH tunnel surfaces
// in seconds instead of after the parent timeout.
func WaitForCephBlockPoolReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, CephBlockPoolGVR, namespace, name,
		timeout, PollTickInterval, "CephBlockPool",
		func(obj *unstructured.Unstructured) (bool, string) {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase == "Ready" {
				return true, "phase=Ready"
			}
			logger.Debug("CephBlockPool %s/%s phase: %q, waiting...", obj.GetNamespace(), obj.GetName(), phase)
			return false, ""
		},
	)
}

// DeleteCephBlockPool deletes a CephBlockPool. Safe to call if the pool does
// not exist.
func DeleteCephBlockPool(ctx context.Context, kubeconfig *rest.Config, namespace, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(CephBlockPoolGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephBlockPool %s/%s: %w", namespace, name, err)
	}
	logger.Info("Deleted CephBlockPool %s/%s", namespace, name)
	return nil
}
