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

// CephClusterGVR is the GroupVersionResource of Rook's CephCluster.
var CephClusterGVR = schema.GroupVersionResource{
	Group:    "ceph.rook.io",
	Version:  "v1",
	Resource: "cephclusters",
}

// Defaults shared between CephClusterConfig and the testkit-level helper.
const (
	DefaultRookNamespace       = "d8-sds-elastic"
	DefaultCephClusterName     = "ceph-cluster"
	DefaultCephImage           = "quay.io/ceph/ceph:v18.2.7"
	DefaultDataDirHostPath     = "/var/lib/rook"
	DefaultOSDStorageClassSize = "10Gi"
)

// CephClusterConfig describes a Rook-managed Ceph cluster suitable for e2e
// testing. It is intentionally narrower than Rook's native CephCluster CRD:
// knobs that don't matter for our scenarios are hidden behind hard-coded
// defaults (mirroring the values from the internal Flant wiki instruction
// on deploying sds-elastic + Rook + Ceph on LVM).
type CephClusterConfig struct {
	// Name of the CephCluster (default: "ceph-cluster").
	Name string

	// Namespace where Rook watches (default: "d8-sds-elastic").
	Namespace string

	// CephImage is the Ceph container image tag.
	// Default: "quay.io/ceph/ceph:v18.2.7".
	CephImage string

	// AllowUnsupportedCephVersion flips spec.cephVersion.allowUnsupported.
	// Default: true (e2e clusters are allowed to run any version Ceph ships).
	AllowUnsupportedCephVersion *bool

	// MonCount / MgrCount are the Rook mon/mgr replica counts. Defaults:
	// 1 / 1, which is appropriate for single-node / tiny test clusters.
	MonCount int
	MgrCount int

	// AllowMultipleMonPerNode allows multiple mons on the same node
	// (required for single-node clusters). Default: true.
	AllowMultipleMonPerNode *bool

	// DataDirHostPath is where Rook persists mon/OSD data on each node.
	// Default: "/var/lib/rook".
	DataDirHostPath string

	// NetworkProvider selects the Rook networking mode. Supported values:
	//   ""      — default CNI pod network (suitable for in-cluster e2e);
	//   "host"  — host networking (matches the Flant wiki production layout).
	NetworkProvider string

	// PublicNetworkCIDRs / ClusterNetworkCIDRs are the public/cluster CIDRs
	// plumbed into `spec.network.addressRanges` when NetworkProvider is
	// non-empty. They are ignored for the default (CNI) mode.
	PublicNetworkCIDRs  []string
	ClusterNetworkCIDRs []string

	// --- OSD backing ---

	// OSDStorageClass is the name of a k8s StorageClass able to hand out
	// block-mode PVCs. Those PVCs are used by Rook's
	// `storage.storageClassDeviceSets` to back OSDs.
	OSDStorageClass string

	// OSDCount is the number of OSDs to provision (default: 1).
	OSDCount int

	// OSDSize is the size of each OSD PVC (default: "10Gi").
	OSDSize string

	// OSDDeviceSetName is the `storageClassDeviceSets[].name` (default:
	// "set1"). Changing it is useful mostly for debugging.
	OSDDeviceSetName string
}

func (c *CephClusterConfig) applyDefaults() {
	if c.Name == "" {
		c.Name = DefaultCephClusterName
	}
	if c.Namespace == "" {
		c.Namespace = DefaultRookNamespace
	}
	if c.CephImage == "" {
		c.CephImage = DefaultCephImage
	}
	if c.AllowUnsupportedCephVersion == nil {
		t := true
		c.AllowUnsupportedCephVersion = &t
	}
	if c.MonCount <= 0 {
		c.MonCount = 1
	}
	if c.MgrCount <= 0 {
		c.MgrCount = 1
	}
	if c.AllowMultipleMonPerNode == nil {
		t := true
		c.AllowMultipleMonPerNode = &t
	}
	if c.DataDirHostPath == "" {
		c.DataDirHostPath = DefaultDataDirHostPath
	}
	if c.OSDCount <= 0 {
		c.OSDCount = 1
	}
	if c.OSDSize == "" {
		c.OSDSize = DefaultOSDStorageClassSize
	}
	if c.OSDDeviceSetName == "" {
		c.OSDDeviceSetName = "set1"
	}
}

// CreateCephCluster creates (or updates) a CephCluster in the given namespace.
// It is idempotent: if the resource already exists, its spec is overwritten
// with the freshly-rendered one so callers can tweak `CephClusterConfig` and
// re-apply without manual cleanup.
func CreateCephCluster(ctx context.Context, kubeconfig *rest.Config, cfg CephClusterConfig) error {
	cfg.applyDefaults()

	if cfg.OSDStorageClass == "" {
		return fmt.Errorf("CephCluster requires OSDStorageClass (backing StorageClass for OSD PVCs)")
	}

	spec := buildCephClusterSpec(cfg)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "ceph.rook.io/v1",
			"kind":       "CephCluster",
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

	logger.Info("Creating CephCluster %s/%s (image=%s, mon=%d, mgr=%d, osd=%d x %s on SC %s)",
		cfg.Namespace, cfg.Name, cfg.CephImage, cfg.MonCount, cfg.MgrCount, cfg.OSDCount, cfg.OSDSize, cfg.OSDStorageClass)

	_, err = dynamicClient.Resource(CephClusterGVR).Namespace(cfg.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err == nil {
		logger.Success("CephCluster %s/%s created", cfg.Namespace, cfg.Name)
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CephCluster %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}

	logger.Info("CephCluster %s/%s already exists, updating spec", cfg.Namespace, cfg.Name)
	existing, err := dynamicClient.Resource(CephClusterGVR).Namespace(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing CephCluster %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	existing.Object["spec"] = spec
	if _, err := dynamicClient.Resource(CephClusterGVR).Namespace(cfg.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CephCluster %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	return nil
}

// buildCephClusterSpec renders the spec portion of a CephCluster object. The
// choice of fields follows the Flant internal wiki instruction for
// sds-elastic + Rook + Ceph, stripped down to the parts that matter in e2e:
//   - mon/mgr counts come from the config (1/1 by default for single-node);
//   - network.provider=host is opt-in via NetworkProvider;
//   - OSDs are backed by one `storageClassDeviceSets[0]` entry that points
//     to a user-supplied StorageClass capable of issuing block-mode PVCs.
func buildCephClusterSpec(cfg CephClusterConfig) map[string]interface{} {
	spec := map[string]interface{}{
		"cephVersion": map[string]interface{}{
			"image":            cfg.CephImage,
			"allowUnsupported": *cfg.AllowUnsupportedCephVersion,
		},
		"dataDirHostPath":                            cfg.DataDirHostPath,
		"skipUpgradeChecks":                          false,
		"continueUpgradeAfterChecksEvenIfNotHealthy": false,
		"mon": map[string]interface{}{
			"count":                int64(cfg.MonCount),
			"allowMultiplePerNode": *cfg.AllowMultipleMonPerNode,
		},
		"mgr": map[string]interface{}{
			"count":                int64(cfg.MgrCount),
			"allowMultiplePerNode": *cfg.AllowMultipleMonPerNode,
			"modules": []interface{}{
				map[string]interface{}{
					"name":    "pg_autoscaler",
					"enabled": true,
				},
			},
		},
		"dashboard": map[string]interface{}{
			"enabled": false,
			"ssl":     false,
		},
		"crashCollector": map[string]interface{}{
			"disable": false,
		},
		"logCollector": map[string]interface{}{
			"enabled":     true,
			"periodicity": "daily",
			"maxLogSize":  "100M",
		},
		"priorityClassNames": map[string]interface{}{
			"mon": "system-node-critical",
			"osd": "system-node-critical",
			"mgr": "system-cluster-critical",
		},
		"disruptionManagement": map[string]interface{}{
			"managePodBudgets":      true,
			"osdMaintenanceTimeout": int64(30),
			"pgHealthCheckTimeout":  int64(0),
		},
		"storage": map[string]interface{}{
			"useAllNodes":   true,
			"useAllDevices": false,
			"storageClassDeviceSets": []interface{}{
				map[string]interface{}{
					"name":            cfg.OSDDeviceSetName,
					"count":           int64(cfg.OSDCount),
					"portable":        false,
					"tuneDeviceClass": true,
					"volumeClaimTemplates": []interface{}{
						map[string]interface{}{
							"metadata": map[string]interface{}{
								"name": "data",
							},
							"spec": map[string]interface{}{
								"resources": map[string]interface{}{
									"requests": map[string]interface{}{
										"storage": cfg.OSDSize,
									},
								},
								"storageClassName": cfg.OSDStorageClass,
								"volumeMode":       "Block",
								"accessModes":      []interface{}{"ReadWriteOnce"},
							},
						},
					},
				},
			},
		},
	}

	if cfg.NetworkProvider != "" {
		network := map[string]interface{}{
			"provider": cfg.NetworkProvider,
			"connections": map[string]interface{}{
				"encryption":   map[string]interface{}{"enabled": false},
				"compression":  map[string]interface{}{"enabled": false},
				"requireMsgr2": false,
			},
		}

		addrs := map[string]interface{}{}
		if len(cfg.PublicNetworkCIDRs) > 0 {
			addrs["public"] = toInterfaceSlice(cfg.PublicNetworkCIDRs)
		}
		if len(cfg.ClusterNetworkCIDRs) > 0 {
			addrs["cluster"] = toInterfaceSlice(cfg.ClusterNetworkCIDRs)
		}
		if len(addrs) > 0 {
			network["addressRanges"] = addrs
		}
		spec["network"] = network
	}

	return spec
}

// toInterfaceSlice converts a []string to a []interface{} so it can be
// embedded into an `unstructured.Unstructured`'s object tree.
func toInterfaceSlice(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// WaitForCephClusterReady blocks until the CephCluster status reports that
// Ceph is up and healthy. Rook exposes the cluster state through two status
// fields:
//   - `status.state` — overall lifecycle phase ("Creating", "Created",
//     "Updating", "Error");
//   - `status.ceph.health` — the Ceph health summary ("HEALTH_OK",
//     "HEALTH_WARN", "HEALTH_ERR"). On a single-OSD test cluster Ceph often
//     sits in HEALTH_WARN (PGs undersized, no replicas), which we still treat
//     as "good enough" as long as `status.state == "Created"`.
//
// We return success once `state == "Created"`. HEALTH_ERR is reported in the
// log and does not short-circuit (Rook may recover).
func WaitForCephClusterReady(ctx context.Context, kubeconfig *rest.Config, namespace, name string, timeout time.Duration) error {
	if namespace == "" || name == "" {
		return fmt.Errorf("namespace and name are required")
	}

	logger.Debug("Waiting for CephCluster %s/%s to reach Created state (timeout: %v)", namespace, name, timeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		obj, err := dynamicClient.Resource(CephClusterGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			state, _, _ := unstructured.NestedString(obj.Object, "status", "state")
			health, _, _ := unstructured.NestedString(obj.Object, "status", "ceph", "health")
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")

			if state == "Created" || phase == "Ready" {
				logger.Success("CephCluster %s/%s is Created (ceph health: %s)", namespace, name, health)
				return nil
			}
			logger.Debug("CephCluster %s/%s state=%q phase=%q health=%q", namespace, name, state, phase, health)
		} else if !apierrors.IsNotFound(err) {
			logger.Debug("Error getting CephCluster %s/%s: %v", namespace, name, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for CephCluster %s/%s: %w", namespace, name, ctx.Err())
		case <-ticker.C:
		}
	}
}

// DeleteCephCluster removes a CephCluster. Tearing down the cluster this way
// is a *destructive* operation — Rook will leave OSD data on host disks under
// `dataDirHostPath` and operator-managed PVCs will not be garbage-collected
// automatically. The operation is still idempotent: a NotFound error is
// swallowed.
func DeleteCephCluster(ctx context.Context, kubeconfig *rest.Config, namespace, name string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if err := dynamicClient.Resource(CephClusterGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete CephCluster %s/%s: %w", namespace, name, err)
	}
	logger.Info("Deleted CephCluster %s/%s", namespace, name)
	return nil
}
