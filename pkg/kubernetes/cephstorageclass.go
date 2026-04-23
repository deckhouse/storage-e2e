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

// CephStorageClassGVR points at csi-ceph's CephStorageClass CR (not to be
// confused with Rook's CephCluster / CephBlockPool).
var CephStorageClassGVR = schema.GroupVersionResource{
	Group:    "storage.deckhouse.io",
	Version:  "v1alpha1",
	Resource: "cephstorageclasses",
}

// Supported CephStorageClass types, mirroring csi-ceph's CRD enum.
const (
	CephStorageClassTypeRBD    = "RBD"
	CephStorageClassTypeCephFS = "CephFS"
)

// CephStorageClassConfig is an intentionally narrow shape tailored for the
// e2e scenarios we care about today — an RBD StorageClass backed by a single
// block pool. CephFS variant is supported but requires FSName+FSPool to be
// set by the caller.
type CephStorageClassConfig struct {
	// Name of the CephStorageClass CR (becomes the k8s StorageClass name).
	Name string

	// ClusterConnectionName points at a CephClusterConnection CR.
	ClusterConnectionName string

	// ClusterAuthenticationName points at a CephClusterAuthentication CR.
	ClusterAuthenticationName string

	// ReclaimPolicy mirrors StorageClass.ReclaimPolicy ("Delete" / "Retain").
	// Default: "Delete".
	ReclaimPolicy string

	// Type is "RBD" (default) or "CephFS".
	Type string

	// --- RBD options (Type == "RBD") ---

	// RBDPool is the Ceph pool name (e.g. "ceph-rbd-r1").
	RBDPool string

	// RBDDefaultFSType picks the filesystem mkfs on volume attach.
	// Default: "ext4".
	RBDDefaultFSType string

	// --- CephFS options (Type == "CephFS") ---
	CephFSName string // Name of the CephFilesystem.
	CephFSPool string // Pool to use inside that filesystem.
}

// CreateCephStorageClass creates (or updates) a CephStorageClass CR. On
// success the csi-ceph controller provisions a corresponding core
// storage.k8s.io/v1 StorageClass in the cluster.
func CreateCephStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg CephStorageClassConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("CephStorageClass name is required")
	}
	if cfg.ClusterConnectionName == "" {
		return fmt.Errorf("CephStorageClass ClusterConnectionName is required")
	}
	if cfg.ClusterAuthenticationName == "" {
		return fmt.Errorf("CephStorageClass ClusterAuthenticationName is required")
	}
	if cfg.Type == "" {
		cfg.Type = CephStorageClassTypeRBD
	}
	if cfg.ReclaimPolicy == "" {
		cfg.ReclaimPolicy = "Delete"
	}

	spec := map[string]interface{}{
		"clusterConnectionName":     cfg.ClusterConnectionName,
		"clusterAuthenticationName": cfg.ClusterAuthenticationName,
		"reclaimPolicy":             cfg.ReclaimPolicy,
		"type":                      cfg.Type,
	}

	switch cfg.Type {
	case CephStorageClassTypeRBD:
		if cfg.RBDPool == "" {
			return fmt.Errorf("CephStorageClass of type RBD requires RBDPool")
		}
		if cfg.RBDDefaultFSType == "" {
			cfg.RBDDefaultFSType = "ext4"
		}
		spec["rbd"] = map[string]interface{}{
			"defaultFSType": cfg.RBDDefaultFSType,
			"pool":          cfg.RBDPool,
		}
	case CephStorageClassTypeCephFS:
		if cfg.CephFSName == "" || cfg.CephFSPool == "" {
			return fmt.Errorf("CephStorageClass of type CephFS requires CephFSName and CephFSPool")
		}
		spec["cephFS"] = map[string]interface{}{
			"fsName": cfg.CephFSName,
			"pool":   cfg.CephFSPool,
		}
	default:
		return fmt.Errorf("unsupported CephStorageClass Type: %s", cfg.Type)
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "storage.deckhouse.io/v1alpha1",
			"kind":       "CephStorageClass",
			"metadata": map[string]interface{}{
				"name": cfg.Name,
			},
			"spec": spec,
		},
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating CephStorageClass %s (type=%s, conn=%s, auth=%s)",
		cfg.Name, cfg.Type, cfg.ClusterConnectionName, cfg.ClusterAuthenticationName)
	_, err = dynamicClient.Resource(CephStorageClassGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephStorageClass %s: %w", cfg.Name, err)
	}

	logger.Info("CephStorageClass %s already exists, updating spec", cfg.Name)
	existing, err := dynamicClient.Resource(CephStorageClassGVR).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch CephStorageClass %s: %w", cfg.Name, err)
	}
	existing.Object["spec"] = spec
	if _, err := dynamicClient.Resource(CephStorageClassGVR).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephStorageClass %s: %w", cfg.Name, err)
	}
	return nil
}

// DeleteCephStorageClass removes a CephStorageClass. NotFound is treated as
// success. The underlying k8s StorageClass is removed by the csi-ceph
// controller as a side effect.
func DeleteCephStorageClass(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	if err := dynamicClient.Resource(CephStorageClassGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephStorageClass %s: %w", name, err)
	}
	logger.Info("Deleted CephStorageClass %s", name)
	return nil
}

// WaitForCephStorageClassCreated polls until the CephStorageClass status
// reports phase=Created (the csi-ceph controller flips this once the backing
// k8s StorageClass has been provisioned).
func WaitForCephStorageClassCreated(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}

	logger.Debug("Waiting for CephStorageClass %s phase=Created (timeout: %v)", name, timeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		obj, err := dynamicClient.Resource(CephStorageClassGVR).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			reason, _, _ := unstructured.NestedString(obj.Object, "status", "reason")
			if phase == "Created" {
				logger.Success("CephStorageClass %s is Created", name)
				return nil
			}
			logger.Debug("CephStorageClass %s phase=%q reason=%q", name, phase, reason)
		} else if !apierrors.IsNotFound(err) {
			logger.Debug("Error getting CephStorageClass %s: %v", name, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for CephStorageClass %s: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}
