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

// CephFilesystemGVR is the GroupVersionResource of Rook's CephFilesystem.
var CephFilesystemGVR = schema.GroupVersionResource{
	Group:    "ceph.rook.io",
	Version:  "v1",
	Resource: "cephfilesystems",
}

// CephFilesystemConfig describes a minimal Rook CephFilesystem with one
// metadata pool and exactly one data pool. Defaults are tuned for tiny
// single-node test clusters and mirror CephBlockPoolConfig conventions.
type CephFilesystemConfig struct {
	// Name of the CephFilesystem CR.
	Name string

	// Namespace the Rook operator watches (typically "d8-sds-elastic").
	Namespace string

	// FailureDomain is the CRUSH failure domain: "host" or "osd"
	// (default: "osd" when MetadataPoolReplicas == DataPoolReplicas == 1,
	// "host" otherwise).
	FailureDomain string

	// MetadataPoolReplicas is the metadata pool replication factor. Default: 1.
	MetadataPoolReplicas int

	// DataPoolName is the (Rook-side) data pool name. The full Ceph pool
	// name is "<Name>-<DataPoolName>" — see CephFSDataPoolFullName.
	// Default: "data0".
	DataPoolName string

	// DataPoolReplicas is the data pool replication factor. Default: 1.
	DataPoolReplicas int

	// MetadataServerActiveCount is the number of active MDS daemons.
	// Default: 1.
	MetadataServerActiveCount int

	// RequireSafeReplicaSize toggles Ceph's safeguard against single-replica
	// pools. When nil, it is set to false for replicas==1 (unsafe single
	// replica, accepted for e2e test clusters) and left unset otherwise.
	RequireSafeReplicaSize *bool
}

// CephFSDataPoolFullName returns the full Ceph pool name that ends up
// referenced from CephStorageClass.spec.cephFS.pool. Rook composes the
// per-filesystem pool name as "<filesystem>-<dataPool.name>".
func CephFSDataPoolFullName(fsName, dataPoolName string) string {
	return fmt.Sprintf("%s-%s", fsName, dataPoolName)
}

// CreateCephFilesystem creates (or updates, if already present) a
// CephFilesystem in the given namespace from the provided configuration. It
// is idempotent and safe to call on every test run.
func CreateCephFilesystem(ctx context.Context, kubeconfig *rest.Config, cfg CephFilesystemConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("CephFilesystem name is required")
	}
	if cfg.Namespace == "" {
		return fmt.Errorf("CephFilesystem namespace is required")
	}
	if cfg.MetadataPoolReplicas <= 0 {
		cfg.MetadataPoolReplicas = 1
	}
	if cfg.DataPoolReplicas <= 0 {
		cfg.DataPoolReplicas = 1
	}
	if cfg.DataPoolName == "" {
		cfg.DataPoolName = "data0"
	}
	if cfg.MetadataServerActiveCount <= 0 {
		cfg.MetadataServerActiveCount = 1
	}
	if cfg.FailureDomain == "" {
		if cfg.MetadataPoolReplicas == 1 && cfg.DataPoolReplicas == 1 {
			cfg.FailureDomain = "osd"
		} else {
			cfg.FailureDomain = "host"
		}
	}

	requireSafe := cfg.RequireSafeReplicaSize
	if requireSafe == nil && (cfg.MetadataPoolReplicas == 1 || cfg.DataPoolReplicas == 1) {
		f := false
		requireSafe = &f
	}

	metadataReplicated := map[string]interface{}{
		"size": int64(cfg.MetadataPoolReplicas),
	}
	dataReplicated := map[string]interface{}{
		"size": int64(cfg.DataPoolReplicas),
	}
	if requireSafe != nil {
		metadataReplicated["requireSafeReplicaSize"] = *requireSafe
		dataReplicated["requireSafeReplicaSize"] = *requireSafe
	}

	spec := map[string]interface{}{
		"metadataPool": map[string]interface{}{
			"failureDomain": cfg.FailureDomain,
			"replicated":    metadataReplicated,
		},
		"dataPools": []interface{}{
			map[string]interface{}{
				"name":          cfg.DataPoolName,
				"failureDomain": cfg.FailureDomain,
				"replicated":    dataReplicated,
			},
		},
		"preserveFilesystemOnDelete": false,
		"metadataServer": map[string]interface{}{
			"activeCount":   int64(cfg.MetadataServerActiveCount),
			"activeStandby": false,
		},
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "ceph.rook.io/v1",
			"kind":       "CephFilesystem",
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

	logger.Info("Creating CephFilesystem %s/%s", cfg.Namespace, cfg.Name)
	_, err = dynamicClient.Resource(CephFilesystemGVR).Namespace(cfg.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		logger.Success("CephFilesystem %s/%s created", cfg.Namespace, cfg.Name)
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephFilesystem %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}

	logger.Info("CephFilesystem %s/%s already exists, updating spec", cfg.Namespace, cfg.Name)
	existing, err := dynamicClient.Resource(CephFilesystemGVR).Namespace(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing CephFilesystem %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	existing.Object["spec"] = spec
	if _, err := dynamicClient.Resource(CephFilesystemGVR).Namespace(cfg.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephFilesystem %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	return nil
}

// WaitForCephFilesystemReady blocks until the CephFilesystem reports
// `status.phase == "Ready"`. As a fallback (some Rook revisions populate
// `status.conditions` first) the function also accepts a Ready=True
// condition.
//
// Per-call deadlines and loud (WARN) logging on consecutive network failures
// are inherited from pollResourceUntilReady.
func WaitForCephFilesystemReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, CephFilesystemGVR, namespace, name,
		timeout, PollTickInterval, "CephFilesystem",
		func(obj *unstructured.Unstructured) (bool, string) {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase == "Ready" {
				return true, "status.phase"
			}
			if cephFilesystemReadyByCondition(obj.Object) {
				return true, "status.conditions[Ready]=True"
			}
			logger.Debug("CephFilesystem %s/%s phase: %q, waiting...", obj.GetNamespace(), obj.GetName(), phase)
			return false, ""
		},
	)
}

func cephFilesystemReadyByCondition(obj map[string]interface{}) bool {
	conditions, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ctype, _, _ := unstructured.NestedString(cond, "type")
		cstatus, _, _ := unstructured.NestedString(cond, "status")
		if ctype == "Ready" && cstatus == "True" {
			return true
		}
	}
	return false
}

// DeleteCephFilesystem deletes a CephFilesystem. Safe to call if the
// filesystem does not exist.
func DeleteCephFilesystem(ctx context.Context, kubeconfig *rest.Config, namespace, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(CephFilesystemGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephFilesystem %s/%s: %w", namespace, name, err)
	}
	logger.Info("Deleted CephFilesystem %s/%s", namespace, name)
	return nil
}
