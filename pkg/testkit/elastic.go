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
	"time"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// Re-exports of the supported ElasticStorageClass enums so callers don't have
// to import the lower-level pkg/kubernetes package just to set cfg.Type /
// cfg.Replication.
const (
	ElasticStorageClassTypeRBD    = kubernetes.ElasticStorageClassTypeRBD
	ElasticStorageClassTypeCephFS = kubernetes.ElasticStorageClassTypeCephFS

	ElasticReplicationAvailabilityWithoutConsistency = kubernetes.ElasticReplicationAvailabilityWithoutConsistency
	ElasticReplicationConsistencyAndAvailability     = kubernetes.ElasticReplicationConsistencyAndAvailability
	ElasticReplicationHighRedundancy                 = kubernetes.ElasticReplicationHighRedundancy
	ElasticReplicationErasureCodedCompact            = kubernetes.ElasticReplicationErasureCodedCompact
)

// Default labels used to mark storage nodes / OSD BlockDevices for an
// ElasticCluster in e2e. The keys are namespaced under a dedicated e2e prefix
// so they never collide with anything the module itself sets.
const (
	DefaultElasticStorageNodeLabelKey   = "sds-elastic-e2e.storage.deckhouse.io/storage-node"
	DefaultElasticStorageNodeLabelValue = "true"
	DefaultElasticOSDLabelKey           = "sds-elastic-e2e.storage.deckhouse.io/osd"
	DefaultElasticOSDLabelValue         = "true"

	// DefaultElasticClusterReadyTimeout covers Rook bringing up the full
	// CephCluster (mon/mgr/osd) on top of LVM-local storage, plus the
	// credential backup and csi-ceph wiring stages of the EC reconcile.
	DefaultElasticClusterReadyTimeout = 25 * time.Minute

	// DefaultElasticStorageClassReadyTimeout covers pool/filesystem
	// provisioning + csi-ceph StorageClass materialisation.
	DefaultElasticStorageClassReadyTimeout = 10 * time.Minute
)

// ElasticOSDBlockDevicesConfig describes how to prepare raw disks for OSD
// adoption by an ElasticCluster: label a set of storage nodes, wait for
// sds-node-configurator to publish consumable BlockDevices on them, then
// label those BlockDevices so the EC's blockDeviceSelector can adopt them.
type ElasticOSDBlockDevicesConfig struct {
	// StorageNodeNames is the explicit set of nodes to use. When empty, all
	// worker nodes are used.
	StorageNodeNames []string

	// NodeLabelKey / NodeLabelValue is applied to every storage node and is
	// what ElasticCluster.spec.storage.nodeSelector.matchLabels should match.
	// Defaults: DefaultElasticStorageNodeLabelKey / DefaultElasticStorageNodeLabelValue.
	NodeLabelKey   string
	NodeLabelValue string

	// BlockDeviceLabelKey / BlockDeviceLabelValue is applied to every
	// consumable BlockDevice found on the storage nodes and is what
	// ElasticCluster.spec.storage.blockDeviceSelector.matchLabels should
	// match. Defaults: DefaultElasticOSDLabelKey / DefaultElasticOSDLabelValue.
	BlockDeviceLabelKey   string
	BlockDeviceLabelValue string

	// MinBlockDevices is the minimum number of consumable BlockDevices that
	// must appear on the storage nodes before labelling proceeds. Default: 1.
	MinBlockDevices int

	// BlockDeviceWaitTimeout bounds the wait for consumable BlockDevices to
	// surface. Default: 10m.
	BlockDeviceWaitTimeout time.Duration
}

func (c *ElasticOSDBlockDevicesConfig) applyDefaults() {
	if c.NodeLabelKey == "" {
		c.NodeLabelKey = DefaultElasticStorageNodeLabelKey
	}
	if c.NodeLabelValue == "" {
		c.NodeLabelValue = DefaultElasticStorageNodeLabelValue
	}
	if c.BlockDeviceLabelKey == "" {
		c.BlockDeviceLabelKey = DefaultElasticOSDLabelKey
	}
	if c.BlockDeviceLabelValue == "" {
		c.BlockDeviceLabelValue = DefaultElasticOSDLabelValue
	}
	if c.MinBlockDevices <= 0 {
		c.MinBlockDevices = 1
	}
	if c.BlockDeviceWaitTimeout == 0 {
		c.BlockDeviceWaitTimeout = 10 * time.Minute
	}
}

const blockDevicePollInterval = 10 * time.Second

// EnsureElasticOSDBlockDevices labels storage nodes and the consumable
// BlockDevices on them so an ElasticCluster can adopt the disks for OSDs.
// Returns the names of the labelled BlockDevices.
//
// Idempotent: re-running re-applies the (already present) labels. It does NOT
// create the ElasticCluster — call EnsureElasticCluster afterwards with
// matching selectors.
func EnsureElasticOSDBlockDevices(ctx context.Context, kubeconfig *rest.Config, cfg ElasticOSDBlockDevicesConfig) ([]string, error) {
	cfg.applyDefaults()

	logger.Step(1, "Resolving storage nodes")
	nodeNames := cfg.StorageNodeNames
	if len(nodeNames) == 0 {
		workers, err := kubernetes.GetWorkerNodes(ctx, kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("list worker nodes: %w", err)
		}
		for i := range workers {
			nodeNames = append(nodeNames, workers[i].Name)
		}
	}
	if len(nodeNames) == 0 {
		return nil, fmt.Errorf("no storage nodes resolved (StorageNodeNames empty and no worker nodes found)")
	}
	logger.StepComplete(1, "Storage nodes: %v", nodeNames)

	logger.Step(2, "Labelling storage nodes with %s=%s", cfg.NodeLabelKey, cfg.NodeLabelValue)
	if err := kubernetes.LabelNodes(ctx, kubeconfig, nodeNames, cfg.NodeLabelKey, cfg.NodeLabelValue); err != nil {
		return nil, fmt.Errorf("label storage nodes: %w", err)
	}
	logger.StepComplete(2, "Storage nodes labelled")

	logger.Step(3, "Waiting for >= %d consumable BlockDevice(s) on storage nodes (timeout %v)",
		cfg.MinBlockDevices, cfg.BlockDeviceWaitTimeout)
	bds, err := waitForConsumableBlockDevicesOnNodes(ctx, kubeconfig, nodeNames, cfg.MinBlockDevices, cfg.BlockDeviceWaitTimeout)
	if err != nil {
		return nil, err
	}
	logger.StepComplete(3, "Found %d consumable BlockDevice(s)", len(bds))

	logger.Step(4, "Labelling %d BlockDevice(s) with %s=%s", len(bds), cfg.BlockDeviceLabelKey, cfg.BlockDeviceLabelValue)
	labelled := make([]string, 0, len(bds))
	for _, bd := range bds {
		if err := kubernetes.LabelBlockDevice(ctx, kubeconfig, bd.Name, cfg.BlockDeviceLabelKey, cfg.BlockDeviceLabelValue); err != nil {
			return nil, fmt.Errorf("label BlockDevice %s: %w", bd.Name, err)
		}
		labelled = append(labelled, bd.Name)
	}
	logger.StepComplete(4, "Labelled BlockDevices: %v", labelled)

	logger.Success("Prepared %d OSD BlockDevice(s) across %d storage node(s)", len(labelled), len(nodeNames))
	return labelled, nil
}

// waitForConsumableBlockDevicesOnNodes polls until at least minCount
// consumable BlockDevices live on the given nodes (or the timeout fires).
func waitForConsumableBlockDevicesOnNodes(ctx context.Context, kubeconfig *rest.Config, nodeNames []string, minCount int, timeout time.Duration) ([]kubernetes.BlockDevice, error) {
	nodeSet := make(map[string]struct{}, len(nodeNames))
	for _, n := range nodeNames {
		nodeSet[n] = struct{}{}
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(blockDevicePollInterval)
	defer ticker.Stop()

	var lastSeen int
	for {
		all, err := kubernetes.GetConsumableBlockDevices(deadlineCtx, kubeconfig)
		if err != nil {
			logger.Debug("listing consumable BlockDevices failed (will retry): %v", err)
		} else {
			var onNodes []kubernetes.BlockDevice
			for _, bd := range all {
				if _, ok := nodeSet[bd.NodeName]; ok {
					onNodes = append(onNodes, bd)
				}
			}
			lastSeen = len(onNodes)
			if lastSeen >= minCount {
				return onNodes, nil
			}
			logger.Debug("consumable BlockDevices on storage nodes: %d/%d", lastSeen, minCount)
		}

		select {
		case <-deadlineCtx.Done():
			return nil, fmt.Errorf("timeout waiting for >= %d consumable BlockDevices on storage nodes (last seen %d): %w",
				minCount, lastSeen, deadlineCtx.Err())
		case <-ticker.C:
		}
	}
}

// ElasticClusterConfig drives EnsureElasticCluster: create an ElasticCluster
// with the given selectors and wait until it is Ready.
type ElasticClusterConfig struct {
	// Name of the ElasticCluster (cluster-scoped). Required.
	Name string

	// NodeSelectorMatchLabels / BlockDeviceSelectorMatchLabels populate the
	// EC storage selectors. They should match the labels applied by
	// EnsureElasticOSDBlockDevices. Required.
	NodeSelectorMatchLabels        map[string]string
	BlockDeviceSelectorMatchLabels map[string]string

	// NetworkPublic / NetworkCluster optionally pin spec.network.
	NetworkPublic  string
	NetworkCluster string

	Labels      map[string]string
	Annotations map[string]string

	// ReadyTimeout bounds the wait for the EC Ready condition.
	// Default: DefaultElasticClusterReadyTimeout.
	ReadyTimeout time.Duration
}

// EnsureElasticCluster creates (or reuses) an ElasticCluster and waits until
// its aggregate Ready condition is True. Returns the EC name.
func EnsureElasticCluster(ctx context.Context, kubeconfig *rest.Config, cfg ElasticClusterConfig) (string, error) {
	if cfg.Name == "" {
		return "", fmt.Errorf("ElasticClusterConfig.Name is required")
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = DefaultElasticClusterReadyTimeout
	}

	logger.Step(1, "Creating ElasticCluster %s", cfg.Name)
	if err := kubernetes.CreateElasticCluster(ctx, kubeconfig, kubernetes.ElasticClusterParams{
		Name:                           cfg.Name,
		NodeSelectorMatchLabels:        cfg.NodeSelectorMatchLabels,
		BlockDeviceSelectorMatchLabels: cfg.BlockDeviceSelectorMatchLabels,
		NetworkPublic:                  cfg.NetworkPublic,
		NetworkCluster:                 cfg.NetworkCluster,
		Labels:                         cfg.Labels,
		Annotations:                    cfg.Annotations,
	}); err != nil {
		return "", fmt.Errorf("create ElasticCluster: %w", err)
	}
	logger.StepComplete(1, "ElasticCluster %s created", cfg.Name)

	logger.Step(2, "Waiting for ElasticCluster %s to become Ready (timeout %v)", cfg.Name, cfg.ReadyTimeout)
	if err := kubernetes.WaitForElasticClusterReady(ctx, kubeconfig, cfg.Name, cfg.ReadyTimeout); err != nil {
		return "", fmt.Errorf("wait ElasticCluster Ready: %w", err)
	}
	logger.StepComplete(2, "ElasticCluster %s is Ready", cfg.Name)

	logger.Success("ElasticCluster %s ready", cfg.Name)
	return cfg.Name, nil
}

// ElasticStorageClassConfig drives EnsureElasticStorageClass.
type ElasticStorageClassConfig struct {
	// Name of the ESC; also the resulting csi-ceph CephStorageClass and core
	// k8s StorageClass name. Required.
	Name string

	// ClusterRef is the owning ElasticCluster. Required.
	ClusterRef string

	// Type selects RBD or CephFS. Required.
	Type string

	// Replication picks the strategy. Empty defaults to the CRD default
	// (ConsistencyAndAvailability).
	Replication string

	Labels      map[string]string
	Annotations map[string]string

	// ReadyTimeout bounds the wait for the ESC Ready condition.
	// Default: DefaultElasticStorageClassReadyTimeout.
	ReadyTimeout time.Duration

	// StorageClassWaitTimeout bounds the extra wait for the core k8s
	// StorageClass to materialise after the ESC is Ready. Default: 2m.
	StorageClassWaitTimeout time.Duration
}

// EnsureElasticStorageClass creates (or reuses) an ElasticStorageClass, waits
// until it is Ready, and confirms the 1:1-named core StorageClass exists.
// Returns the StorageClass name (== ESC name).
func EnsureElasticStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg ElasticStorageClassConfig) (string, error) {
	if cfg.Name == "" {
		return "", fmt.Errorf("ElasticStorageClassConfig.Name is required")
	}
	if cfg.ClusterRef == "" {
		return "", fmt.Errorf("ElasticStorageClassConfig.ClusterRef is required")
	}
	if cfg.Type == "" {
		return "", fmt.Errorf("ElasticStorageClassConfig.Type is required (RBD or CephFS)")
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = DefaultElasticStorageClassReadyTimeout
	}
	if cfg.StorageClassWaitTimeout == 0 {
		cfg.StorageClassWaitTimeout = 2 * time.Minute
	}

	logger.Step(1, "Creating ElasticStorageClass %s (clusterRef=%s, type=%s, replication=%s)",
		cfg.Name, cfg.ClusterRef, cfg.Type, cfg.Replication)
	if err := kubernetes.CreateElasticStorageClass(ctx, kubeconfig, kubernetes.ElasticStorageClassParams{
		Name:        cfg.Name,
		ClusterRef:  cfg.ClusterRef,
		Type:        cfg.Type,
		Replication: cfg.Replication,
		Labels:      cfg.Labels,
		Annotations: cfg.Annotations,
	}); err != nil {
		return "", fmt.Errorf("create ElasticStorageClass: %w", err)
	}
	logger.StepComplete(1, "ElasticStorageClass %s created", cfg.Name)

	logger.Step(2, "Waiting for ElasticStorageClass %s to become Ready (timeout %v)", cfg.Name, cfg.ReadyTimeout)
	if err := kubernetes.WaitForElasticStorageClassReady(ctx, kubeconfig, cfg.Name, cfg.ReadyTimeout); err != nil {
		return "", fmt.Errorf("wait ElasticStorageClass Ready: %w", err)
	}
	logger.StepComplete(2, "ElasticStorageClass %s is Ready", cfg.Name)

	logger.Step(3, "Waiting for core StorageClass %s to materialise", cfg.Name)
	if err := kubernetes.WaitForStorageClass(ctx, kubeconfig, cfg.Name, cfg.StorageClassWaitTimeout); err != nil {
		return "", fmt.Errorf("wait core StorageClass: %w", err)
	}
	logger.StepComplete(3, "StorageClass %s is available", cfg.Name)

	logger.Success("ElasticStorageClass %s ready (type=%s)", cfg.Name, cfg.Type)
	return cfg.Name, nil
}

// TeardownElasticStorageClass deletes an ElasticStorageClass and waits until
// it is fully gone. When force is true, the destructive force-deletion
// annotation is set first (authorising the purge of a non-empty RBD pool); it
// never bypasses the bound-PV guard. Safe to call on missing resources.
func TeardownElasticStorageClass(ctx context.Context, kubeconfig *rest.Config, name string, force bool, timeout time.Duration) error {
	if force {
		logger.Info("Setting force-deletion annotation on ElasticStorageClass %s", name)
		if err := kubernetes.AnnotateElasticStorageClassForceDeletion(ctx, kubeconfig, name); err != nil {
			return fmt.Errorf("annotate ElasticStorageClass force-deletion: %w", err)
		}
	}
	if err := kubernetes.DeleteElasticStorageClass(ctx, kubeconfig, name); err != nil {
		return fmt.Errorf("delete ElasticStorageClass: %w", err)
	}
	if err := kubernetes.WaitForElasticStorageClassGone(ctx, kubeconfig, name, timeout); err != nil {
		return fmt.Errorf("wait ElasticStorageClass gone: %w", err)
	}
	logger.Success("ElasticStorageClass %s torn down", name)
	return nil
}

// TeardownElasticCluster deletes an ElasticCluster and waits until it is fully
// gone (the controller finalizer first tears down the whole Rook CephCluster
// and the csi-ceph wiring). Safe to call on missing resources. Tear down all
// referencing ElasticStorageClasses first — otherwise the EC sticks on the
// non-bypassable StorageClassesExist guard.
func TeardownElasticCluster(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	if err := kubernetes.DeleteElasticCluster(ctx, kubeconfig, name); err != nil {
		return fmt.Errorf("delete ElasticCluster: %w", err)
	}
	if err := kubernetes.WaitForElasticClusterGone(ctx, kubeconfig, name, timeout); err != nil {
		return fmt.Errorf("wait ElasticCluster gone: %w", err)
	}
	logger.Success("ElasticCluster %s torn down", name)
	return nil
}
