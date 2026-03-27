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

package testkit

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// DefaultStorageClassConfig configures CreateDefaultStorageClass behavior.
type DefaultStorageClassConfig struct {
	// StorageClassName is the name for the created LocalStorageClass (and the resulting StorageClass).
	StorageClassName string

	// LVMType is "Thick" or "Thin" (default: "Thin").
	LVMType string

	// ThinPoolName is required when LVMType is "Thin".
	ThinPoolName string

	// ThinPoolSize is the size of the thin pool (e.g. "70%", "90%", "10Gi"). Default: "90%".
	ThinPoolSize string

	// ThinPoolAllocationLimit sets the overprovisioning limit for the thin pool (e.g. "150%"). Default: "150%".
	ThinPoolAllocationLimit string

	// VGName is the LVM Volume Group name to create on each node (default: "vg-local").
	VGName string

	// NodeNames lists nodes on which to create LVGs.
	// If empty, nodes are discovered automatically (workers only, unless IncludeMasters is set).
	NodeNames []string

	// IncludeMasters, when true, includes master nodes in the target list.
	// Nodes with NoSchedule/NoExecute taints will still receive VirtualDisks and labels,
	// but BlockDevice/LVG creation is skipped for them (the DaemonSet won't schedule there).
	IncludeMasters bool

	// --- VM disk attachment (optional) ---
	// When BaseKubeconfig is set, VirtualDisks are created and attached to worker VMs
	// on the base (hypervisor) cluster before waiting for BlockDevices.
	// If nil, disk attachment is skipped (disks must be pre-provisioned).
	BaseKubeconfig *rest.Config

	// VMNamespace is the namespace in the base cluster where VMs reside.
	// Required when BaseKubeconfig is set.
	VMNamespace string

	// BaseStorageClassName is the StorageClass on the base cluster used for VirtualDisk PVCs.
	// Required when BaseKubeconfig is set.
	BaseStorageClassName string

	// DiskSize is the size of each VirtualDisk to attach (default: "20Gi").
	DiskSize string

	// --- Timeouts ---

	// DiskAttachTimeout is how long to wait for all disk attachments (default: 15m).
	DiskAttachTimeout time.Duration

	// BlockDeviceWaitTimeout is how long to wait for consumable block devices per node (default: 10m).
	BlockDeviceWaitTimeout time.Duration

	// LVGReadyTimeout is how long to wait for each LVG to become Ready (default: 10m).
	LVGReadyTimeout time.Duration

	// LocalStorageClassTimeout is how long to wait for LocalStorageClass to reach Created phase (default: 5m).
	LocalStorageClassTimeout time.Duration

	// StorageClassWaitTimeout is how long to wait for the resulting StorageClass to appear (default: 2m).
	StorageClassWaitTimeout time.Duration
}

func (c *DefaultStorageClassConfig) applyDefaults() {
	if c.LVMType == "" {
		c.LVMType = "Thin"
	}
	if c.VGName == "" {
		c.VGName = "vg-local"
	}
	if c.ThinPoolName == "" {
		c.ThinPoolName = "thinpool"
	}
	if c.ThinPoolSize == "" {
		c.ThinPoolSize = "90%"
	}
	if c.ThinPoolAllocationLimit == "" {
		c.ThinPoolAllocationLimit = "150%"
	}
	if c.DiskSize == "" {
		c.DiskSize = "20Gi"
	}
	if c.DiskAttachTimeout == 0 {
		c.DiskAttachTimeout = 15 * time.Minute
	}
	if c.BlockDeviceWaitTimeout == 0 {
		c.BlockDeviceWaitTimeout = 10 * time.Minute
	}
	if c.LVGReadyTimeout == 0 {
		c.LVGReadyTimeout = 10 * time.Minute
	}
	if c.LocalStorageClassTimeout == 0 {
		c.LocalStorageClassTimeout = 5 * time.Minute
	}
	if c.StorageClassWaitTimeout == 0 {
		c.StorageClassWaitTimeout = 2 * time.Minute
	}
}

// CreateDefaultStorageClass is a high-level helper that:
//  1. Discovers target nodes (workers by default; all nodes when IncludeMasters is set).
//  2. Enables sds-node-configurator and sds-local-volume modules.
//  3. Labels target nodes so the sds-node-configurator DaemonSet schedules agents.
//  4. (Optional) If BaseKubeconfig is set, attaches VirtualDisks to target VMs.
//  5. On each schedulable node: waits for consumable BlockDevices, then creates an LVMVolumeGroup.
//     Nodes with NoSchedule/NoExecute taints are skipped (DaemonSet won't schedule there).
//  6. Creates a LocalStorageClass CR referencing the created LVGs.
//  7. Waits for the sds-local-volume controller to create the corresponding StorageClass.
func CreateDefaultStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg DefaultStorageClassConfig) (string, error) {
	cfg.applyDefaults()

	if cfg.StorageClassName == "" {
		return "", fmt.Errorf("StorageClassName is required")
	}
	if cfg.LVMType != "Thin" && cfg.LVMType != "Thick" {
		return "", fmt.Errorf("invalid LVMType: %s (must be Thin or Thick)", cfg.LVMType)
	}
	if cfg.LVMType == "Thin" && cfg.ThinPoolName == "" {
		return "", fmt.Errorf("ThinPoolName is required for Thin LVM type")
	}

	// 1. Resolve node list.
	nodes := cfg.NodeNames
	if len(nodes) == 0 {
		var nodeObjs []corev1.Node
		var err error
		if cfg.IncludeMasters {
			nodeObjs, err = kubernetes.GetNodes(ctx, kubeconfig)
			if err != nil {
				return "", fmt.Errorf("failed to get all nodes: %w", err)
			}
		} else {
			nodeObjs, err = kubernetes.GetWorkerNodes(ctx, kubeconfig)
			if err != nil {
				return "", fmt.Errorf("failed to get worker nodes: %w", err)
			}
		}
		for _, n := range nodeObjs {
			nodes = append(nodes, n.Name)
		}
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("no nodes available for LVG creation")
	}
	logger.Info("Target nodes (%d): %v", len(nodes), nodes)

	// 2. Enable sds-node-configurator and sds-local-volume modules.
	storageModules := []kubernetes.ModuleSpec{
		{
			Name:    "sds-node-configurator",
			Version: 1,
			Enabled: true,
		},
		{
			Name:         "sds-local-volume",
			Version:      1,
			Enabled:      true,
			Dependencies: []string{"sds-node-configurator"},
		},
	}
	if err := kubernetes.EnableModulesAndWait(ctx, kubeconfig, nil, nil, storageModules, 10*time.Minute); err != nil {
		return "", fmt.Errorf("failed to enable storage modules: %w", err)
	}

	// 3. Label all nodes so sds-node-configurator DaemonSet schedules agents.
	const sdsLocalVolumeNodeLabel = "storage.deckhouse.io/sds-local-volume-node"
	if err := kubernetes.LabelNodes(ctx, kubeconfig, nodes, sdsLocalVolumeNodeLabel, ""); err != nil {
		return "", fmt.Errorf("failed to label nodes for sds-node-configurator: %w", err)
	}

	// 4. Attach VirtualDisks to all VMs including masters (VM-based clusters only).
	if cfg.BaseKubeconfig != nil {
		if cfg.VMNamespace == "" {
			return "", fmt.Errorf("VMNamespace is required when BaseKubeconfig is set")
		}
		if cfg.BaseStorageClassName == "" {
			return "", fmt.Errorf("BaseStorageClassName is required when BaseKubeconfig is set")
		}

		logger.Info("Attaching VirtualDisks to %d VMs", len(nodes))
		attachCtx, attachCancel := context.WithTimeout(ctx, cfg.DiskAttachTimeout)
		defer attachCancel()

		runSuffix := time.Now().Unix()
		for _, nodeName := range nodes {
			diskName := fmt.Sprintf("%s-sds-local-disk-%d", nodeName, runSuffix)
			res, err := kubernetes.AttachVirtualDiskToVM(attachCtx, cfg.BaseKubeconfig, kubernetes.VirtualDiskAttachmentConfig{
				VMName:           nodeName,
				Namespace:        cfg.VMNamespace,
				DiskName:         diskName,
				DiskSize:         cfg.DiskSize,
				StorageClassName: cfg.BaseStorageClassName,
			})
			if err != nil {
				return "", fmt.Errorf("failed to attach VirtualDisk to VM %s: %w", nodeName, err)
			}

			if err := kubernetes.WaitForVirtualDiskAttached(attachCtx, cfg.BaseKubeconfig, cfg.VMNamespace, res.AttachmentName, 10*time.Second); err != nil {
				return "", fmt.Errorf("disk attachment for VM %s did not complete: %w", nodeName, err)
			}
		}
		logger.Success("All VirtualDisks attached")
	}

	// 5. For each node: wait for block devices → create LVG → wait for Ready.
	//    Nodes with NoSchedule/NoExecute taints are skipped (agent DaemonSet won't schedule there).
	var lvgNames []string
	for _, nodeName := range nodes {
		if cfg.IncludeMasters {
			cordoned, err := kubernetes.IsNodeCordoned(ctx, kubeconfig, nodeName)
			if err != nil {
				logger.Warn("Could not check taints for node %s: %v, attempting LVG setup anyway", nodeName, err)
			} else if cordoned {
				logger.Warn("Skipping LVG setup on node %s: has NoSchedule/NoExecute taint (agent DaemonSet won't schedule)", nodeName)
				continue
			}
		}

		logger.Info("Setting up LVG on node %s", nodeName)

		var bds []kubernetes.BlockDevice
		deadline := time.Now().Add(cfg.BlockDeviceWaitTimeout)
		for {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout waiting for consumable block devices on node %s", nodeName)
			}

			var err error
			bds, err = kubernetes.GetConsumableBlockDevicesByNode(ctx, kubeconfig, nodeName)
			if err != nil {
				logger.Debug("Error getting block devices on %s: %v, retrying...", nodeName, err)
				time.Sleep(10 * time.Second)
				continue
			}
			if len(bds) > 0 {
				break
			}
			logger.Debug("No consumable block devices on %s yet, retrying...", nodeName)
			time.Sleep(10 * time.Second)
		}
		logger.Info("Found %d consumable block device(s) on node %s", len(bds), nodeName)

		lvgName := fmt.Sprintf("lvg-%s", nodeName)
		var err error
		switch cfg.LVMType {
		case "Thin":
			err = kubernetes.CreateLVMVolumeGroupWithThinPool(ctx, kubeconfig, lvgName, nodeName, []string{bds[0].Name}, cfg.VGName, []kubernetes.ThinPoolSpec{
				{
					Name:            cfg.ThinPoolName,
					Size:            cfg.ThinPoolSize,
					AllocationLimit: cfg.ThinPoolAllocationLimit,
				},
			})
		case "Thick":
			err = kubernetes.CreateLVMVolumeGroup(ctx, kubeconfig, lvgName, nodeName, []string{bds[0].Name}, cfg.VGName)
		}
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("failed to create LVG %s: %w", lvgName, err)
		}

		if err := kubernetes.WaitForLVMVolumeGroupReady(ctx, kubeconfig, lvgName, cfg.LVGReadyTimeout); err != nil {
			return "", fmt.Errorf("LVG %s did not become ready: %w", lvgName, err)
		}

		lvgNames = append(lvgNames, lvgName)
	}

	// 6. Create LocalStorageClass CR referencing all created LVGs.
	err := kubernetes.CreateLocalStorageClass(ctx, kubeconfig, kubernetes.LocalStorageClassConfig{
		Name:            cfg.StorageClassName,
		LVMVolumeGroups: lvgNames,
		LVMType:         cfg.LVMType,
		ThinPoolName:    cfg.ThinPoolName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create LocalStorageClass %s: %w", cfg.StorageClassName, err)
	}

	// 7. Wait for the controller to process LocalStorageClass and create StorageClass.
	if err := kubernetes.WaitForLocalStorageClassCreated(ctx, kubeconfig, cfg.StorageClassName, cfg.LocalStorageClassTimeout); err != nil {
		return "", fmt.Errorf("LocalStorageClass %s did not reach Created phase: %w", cfg.StorageClassName, err)
	}

	if err := kubernetes.WaitForStorageClass(ctx, kubeconfig, cfg.StorageClassName, cfg.StorageClassWaitTimeout); err != nil {
		return "", fmt.Errorf("StorageClass %s did not appear: %w", cfg.StorageClassName, err)
	}

	logger.Success("StorageClass %s created via LocalStorageClass with VG %s on %d nodes", cfg.StorageClassName, cfg.VGName, len(nodes))
	return cfg.StorageClassName, nil
}

// EnsureDefaultStorageClass is an idempotent wrapper around CreateDefaultStorageClass.
// It first checks whether the requested StorageClass already exists. If it does,
// it skips creation. In either case it configures the StorageClass as the cluster
// default via the "global" ModuleConfig (spec.settings.storageClass).
//
// Returns the StorageClass name and any error encountered.
func EnsureDefaultStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg DefaultStorageClassConfig) (string, error) {
	cfg.applyDefaults()

	if cfg.StorageClassName == "" {
		return "", fmt.Errorf("StorageClassName is required")
	}

	existingSC, err := kubernetes.GetStorageClass(ctx, kubeconfig, cfg.StorageClassName)
	if err != nil {
		return "", fmt.Errorf("failed to check StorageClass %s: %w", cfg.StorageClassName, err)
	}

	var scName string
	if existingSC != nil {
		logger.Info("StorageClass %s already exists, skipping creation", cfg.StorageClassName)
		scName = cfg.StorageClassName
	} else {
		scName, err = CreateDefaultStorageClass(ctx, kubeconfig, cfg)
		if err != nil {
			return "", fmt.Errorf("failed to create StorageClass %s: %w", cfg.StorageClassName, err)
		}
	}

	if err := kubernetes.SetGlobalDefaultStorageClass(ctx, kubeconfig, scName); err != nil {
		return "", fmt.Errorf("failed to set %s as default in global ModuleConfig: %w", scName, err)
	}
	logger.Success("StorageClass %s is set as the cluster default", scName)

	return scName, nil
}
