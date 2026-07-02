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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// The sds-elastic module ships a vendored Rook operator whose API group is
// renamed from the upstream ceph.rook.io to internal.sdselastic.deckhouse.io
// (see sds-elastic images/operator/patches). The verifiers below address the
// renamed group so the e2e suite can assert that the Rook resources the
// sds-elastic controller created are healthy AND that no upstream ceph.rook.io
// objects leaked onto the cluster (handled at the suite level via discovery).
const (
	// ElasticRookGroup is the renamed Rook API group used by sds-elastic.
	ElasticRookGroup = "internal.sdselastic.deckhouse.io"
	// ElasticRookVersion is the renamed Rook API version.
	ElasticRookVersion = "v1"
	// UpstreamRookGroup is the upstream Rook API group that MUST NOT appear
	// on a cluster running sds-elastic.
	UpstreamRookGroup = "ceph.rook.io"
)

var (
	// ElasticRookCephClusterGVR is the renamed-group CephCluster GVR.
	ElasticRookCephClusterGVR = schema.GroupVersionResource{
		Group:    ElasticRookGroup,
		Version:  ElasticRookVersion,
		Resource: "cephclusters",
	}
	// ElasticRookCephBlockPoolGVR is the renamed-group CephBlockPool GVR.
	ElasticRookCephBlockPoolGVR = schema.GroupVersionResource{
		Group:    ElasticRookGroup,
		Version:  ElasticRookVersion,
		Resource: "cephblockpools",
	}
	// ElasticRookCephFilesystemGVR is the renamed-group CephFilesystem GVR.
	ElasticRookCephFilesystemGVR = schema.GroupVersionResource{
		Group:    ElasticRookGroup,
		Version:  ElasticRookVersion,
		Resource: "cephfilesystems",
	}
)

// WaitForElasticRookCephClusterReady blocks until the renamed-group
// CephCluster reports state=Created (or phase=Ready). Mirrors the readiness
// logic of WaitForCephClusterReady but against internal.sdselastic.deckhouse.io.
func WaitForElasticRookCephClusterReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, ElasticRookCephClusterGVR, namespace, name,
		timeout, 10*time.Second, "Rook CephCluster",
		func(obj *unstructured.Unstructured) (bool, string) {
			state, _, _ := unstructured.NestedString(obj.Object, "status", "state")
			health, _, _ := unstructured.NestedString(obj.Object, "status", "ceph", "health")
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if state == "Created" || phase == "Ready" {
				return true, fmt.Sprintf("state=%s phase=%s ceph health: %s", state, phase, health)
			}
			logger.Debug("Rook CephCluster %s/%s state=%q phase=%q health=%q",
				obj.GetNamespace(), obj.GetName(), state, phase, health)
			return false, ""
		},
	)
}

// WaitForElasticRookCephBlockPoolReady blocks until the renamed-group
// CephBlockPool reports status.phase=Ready.
func WaitForElasticRookCephBlockPoolReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, ElasticRookCephBlockPoolGVR, namespace, name,
		timeout, PollTickInterval, "Rook CephBlockPool",
		func(obj *unstructured.Unstructured) (bool, string) {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase == "Ready" {
				return true, "phase=Ready"
			}
			logger.Debug("Rook CephBlockPool %s/%s phase=%q", obj.GetNamespace(), obj.GetName(), phase)
			return false, ""
		},
	)
}

// WaitForElasticRookCephFilesystemReady blocks until the renamed-group
// CephFilesystem reports status.phase=Ready.
func WaitForElasticRookCephFilesystemReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	return pollResourceUntilReady(
		ctx, kubeconfig, ElasticRookCephFilesystemGVR, namespace, name,
		timeout, PollTickInterval, "Rook CephFilesystem",
		func(obj *unstructured.Unstructured) (bool, string) {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase == "Ready" {
				return true, "phase=Ready"
			}
			logger.Debug("Rook CephFilesystem %s/%s phase=%q", obj.GetNamespace(), obj.GetName(), phase)
			return false, ""
		},
	)
}

// ListElasticRookCephClusterNames returns the names of all renamed-group
// CephClusters in the namespace. Used to assert the sds-elastic controller
// created exactly the CephCluster(s) it should have.
func ListElasticRookCephClusterNames(ctx context.Context, kubeconfig *rest.Config, namespace string) ([]string, error) {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	list, err := dynamicClient.Resource(ElasticRookCephClusterGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Rook CephClusters in %s: %w", namespace, err)
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].GetName())
	}
	return names, nil
}

// ServerHasAPIGroup reports whether the apiserver advertises the given API
// group in discovery. The e2e suite uses it to assert that the upstream
// ceph.rook.io group is absent on a cluster running sds-elastic (the module
// renames Rook to internal.sdselastic.deckhouse.io to avoid clobbering a
// user-installed upstream Rook).
func ServerHasAPIGroup(ctx context.Context, kubeconfig *rest.Config, group string) (bool, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %w", err)
	}
	groups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		return false, fmt.Errorf("failed to list server API groups: %w", err)
	}
	for i := range groups.Groups {
		if groups.Groups[i].Name == group {
			return true, nil
		}
	}
	return false, nil
}
