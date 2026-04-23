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

// GVRs of the csi-ceph cluster-scoped CRs. We use unstructured to avoid
// pulling github.com/deckhouse/csi-ceph/api into go.mod just for these
// tiny types.
var (
	CephClusterConnectionGVR = schema.GroupVersionResource{
		Group:    "storage.deckhouse.io",
		Version:  "v1alpha1",
		Resource: "cephclusterconnections",
	}
	CephClusterAuthenticationGVR = schema.GroupVersionResource{
		Group:    "storage.deckhouse.io",
		Version:  "v1alpha1",
		Resource: "cephclusterauthentications",
	}
)

// CephClusterAuthenticationConfig describes CephX credentials that csi-ceph
// reuses for every StorageClass that references the authentication.
type CephClusterAuthenticationConfig struct {
	// Name of the CephClusterAuthentication CR.
	Name string
	// UserID is the Ceph user (typically "admin").
	UserID string
	// UserKey is the CephX key of UserID.
	UserKey string
}

// CreateCephClusterAuthentication creates (or updates) a
// CephClusterAuthentication CR with the given CephX credentials.
func CreateCephClusterAuthentication(ctx context.Context, kubeconfig *rest.Config, cfg CephClusterAuthenticationConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("CephClusterAuthentication name is required")
	}
	if cfg.UserID == "" {
		return fmt.Errorf("CephClusterAuthentication UserID is required")
	}
	if cfg.UserKey == "" {
		return fmt.Errorf("CephClusterAuthentication UserKey is required")
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "storage.deckhouse.io/v1alpha1",
			"kind":       "CephClusterAuthentication",
			"metadata": map[string]interface{}{
				"name": cfg.Name,
			},
			"spec": map[string]interface{}{
				"userID":  cfg.UserID,
				"userKey": cfg.UserKey,
			},
		},
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating CephClusterAuthentication %s (userID=%s)", cfg.Name, cfg.UserID)
	_, err = dynamicClient.Resource(CephClusterAuthenticationGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephClusterAuthentication %s: %w", cfg.Name, err)
	}

	logger.Info("CephClusterAuthentication %s already exists, updating spec", cfg.Name)
	existing, err := dynamicClient.Resource(CephClusterAuthenticationGVR).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch CephClusterAuthentication %s: %w", cfg.Name, err)
	}
	existing.Object["spec"] = obj.Object["spec"]
	if _, err := dynamicClient.Resource(CephClusterAuthenticationGVR).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephClusterAuthentication %s: %w", cfg.Name, err)
	}
	return nil
}

// DeleteCephClusterAuthentication removes a CephClusterAuthentication.
// NotFound is treated as success.
func DeleteCephClusterAuthentication(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	if err := dynamicClient.Resource(CephClusterAuthenticationGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephClusterAuthentication %s: %w", name, err)
	}
	logger.Info("Deleted CephClusterAuthentication %s", name)
	return nil
}

// CephClusterConnectionConfig describes a csi-ceph CephClusterConnection CR.
// Its spec.clusterID (== Ceph fsid) is immutable once created.
type CephClusterConnectionConfig struct {
	// Name of the CephClusterConnection CR.
	Name string
	// ClusterID is the Ceph fsid. Immutable after creation.
	ClusterID string
	// Monitors is the list of `ip:port` monitor endpoints.
	Monitors []string
	// UserID is the Ceph user (typically "admin").
	UserID string
	// UserKey is the CephX key of UserID.
	UserKey string
}

// CreateCephClusterConnection creates (or updates) a CephClusterConnection CR.
// If the resource already exists we do *not* attempt to update spec.clusterID
// (which the CRD marks immutable) — only Monitors/UserID/UserKey are synced.
func CreateCephClusterConnection(ctx context.Context, kubeconfig *rest.Config, cfg CephClusterConnectionConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("CephClusterConnection name is required")
	}
	if cfg.ClusterID == "" {
		return fmt.Errorf("CephClusterConnection ClusterID (fsid) is required")
	}
	if len(cfg.Monitors) == 0 {
		return fmt.Errorf("CephClusterConnection Monitors is required")
	}

	monitors := make([]interface{}, len(cfg.Monitors))
	for i, m := range cfg.Monitors {
		monitors[i] = m
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "storage.deckhouse.io/v1alpha1",
			"kind":       "CephClusterConnection",
			"metadata": map[string]interface{}{
				"name": cfg.Name,
			},
			"spec": map[string]interface{}{
				"clusterID": cfg.ClusterID,
				"monitors":  monitors,
				"userID":    cfg.UserID,
				"userKey":   cfg.UserKey,
			},
		},
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	logger.Info("Creating CephClusterConnection %s (clusterID=%s, mons=%d)", cfg.Name, cfg.ClusterID, len(cfg.Monitors))
	_, err = dynamicClient.Resource(CephClusterConnectionGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephClusterConnection %s: %w", cfg.Name, err)
	}

	logger.Info("CephClusterConnection %s already exists, syncing monitors/userID/userKey", cfg.Name)
	existing, err := dynamicClient.Resource(CephClusterConnectionGVR).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch CephClusterConnection %s: %w", cfg.Name, err)
	}
	if err := unstructured.SetNestedSlice(existing.Object, monitors, "spec", "monitors"); err != nil {
		return fmt.Errorf("set monitors: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, cfg.UserID, "spec", "userID"); err != nil {
		return fmt.Errorf("set userID: %w", err)
	}
	if err := unstructured.SetNestedField(existing.Object, cfg.UserKey, "spec", "userKey"); err != nil {
		return fmt.Errorf("set userKey: %w", err)
	}
	if _, err := dynamicClient.Resource(CephClusterConnectionGVR).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephClusterConnection %s: %w", cfg.Name, err)
	}
	return nil
}

// DeleteCephClusterConnection removes a CephClusterConnection.
// NotFound is treated as success.
func DeleteCephClusterConnection(ctx context.Context, kubeconfig *rest.Config, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	if err := dynamicClient.Resource(CephClusterConnectionGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephClusterConnection %s: %w", name, err)
	}
	logger.Info("Deleted CephClusterConnection %s", name)
	return nil
}

// WaitForCephClusterConnectionCreated polls until the CephClusterConnection
// status reports phase=Created. csi-ceph's controller flips the status from
// Pending to Created once it has verified the supplied fsid / monitors /
// CephX credentials against the real Ceph cluster.
func WaitForCephClusterConnectionCreated(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}

	logger.Debug("Waiting for CephClusterConnection %s phase=Created (timeout: %v)", name, timeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		obj, err := dynamicClient.Resource(CephClusterConnectionGVR).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			reason, _, _ := unstructured.NestedString(obj.Object, "status", "reason")
			if phase == "Created" {
				logger.Success("CephClusterConnection %s is Created", name)
				return nil
			}
			logger.Debug("CephClusterConnection %s phase=%q reason=%q", name, phase, reason)
		} else if !apierrors.IsNotFound(err) {
			logger.Debug("Error getting CephClusterConnection %s: %v", name, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for CephClusterConnection %s: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}
