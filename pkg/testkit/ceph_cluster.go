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

// RookCephClusterConfig configures EnsureCephCluster — the "just bring up
// a Rook-managed Ceph cluster + pool" variant of EnsureCephStorageClass.
//
// Unlike EnsureCephStorageClass, EnsureCephCluster does NOT:
//   - enable the `csi-ceph` Deckhouse module;
//   - create CephClusterConnection / CephClusterAuthentication CRs;
//   - create a CephStorageClass CR / materialize a core StorageClass.
//
// It stops once the Rook CephCluster is Created and the CephBlockPool is
// Ready. Use this when the test suite needs a live Ceph backend to exercise
// (e.g. to run rbd / ceph CLI against it, or to hook some other client) but
// deliberately does NOT want csi-ceph in the picture.
type RookCephClusterConfig struct {
	// --- Namespacing / naming ---

	// Namespace is the Rook / sds-elastic namespace. Default: "d8-sds-elastic".
	Namespace string

	// CephClusterName is the Rook CephCluster name. Default: "ceph-cluster".
	CephClusterName string

	// CephImage is the Ceph container image. Default:
	// "quay.io/ceph/ceph:v18.2.7".
	CephImage string

	// MonCount / MgrCount are the Rook mon/mgr replica counts.
	// Defaults: 1 / 1 (appropriate for 1..3-node test clusters).
	MonCount int
	MgrCount int

	// NetworkProvider: "" for CNI (default), "host" for host networking.
	NetworkProvider     string
	PublicNetworkCIDRs  []string
	ClusterNetworkCIDRs []string

	// GlobalCephConfigOverrides populates `rook-config-override` under
	// `[global]`, e.g. {"ms_crc_data": "false"} for the csi-ceph
	// msCrcData matrix. nil leaves the ConfigMap otherwise empty.
	GlobalCephConfigOverrides map[string]string

	// --- OSD backing ---

	// OSDStorageClass is a block-capable StorageClass used to back OSD PVCs.
	// When empty, EnsureDefaultStorageClass is invoked with
	// OSDBacking* to provision a sds-local-volume SC on the fly.
	OSDStorageClass string

	// OSDCount is the number of OSDs. Default: 1.
	OSDCount int

	// OSDSize is the size of each OSD PVC. Default: kubernetes.DefaultOSDStorageClassSize.
	OSDSize string

	// --- Fallback SC provisioning via sds-local-volume ---

	// OSDBackingStorageClassName names the sds-local-volume SC we auto-
	// provision for OSDs. Default: "sds-local-volume-thick-ceph-osd".
	OSDBackingStorageClassName string

	// OSDBackingLVMType ("Thick"/"Thin"). Default: "Thick".
	OSDBackingLVMType string

	OSDBackingIncludeMasters       bool
	OSDBackingBaseKubeconfig       *rest.Config
	OSDBackingVMNamespace          string
	OSDBackingBaseStorageClassName string

	// --- CephBlockPool ---

	// PoolName is the Rook CephBlockPool name. Default:
	// "ceph-rbd-r<ReplicaSize>".
	PoolName string

	// ReplicaSize is the CephBlockPool replication factor. Default: 1.
	ReplicaSize int

	// FailureDomain: "host" or "osd". Default: "osd" when ReplicaSize==1,
	// "host" otherwise.
	FailureDomain string

	// --- Modules ---

	// SkipModuleEnablement disables the module-enable step (useful when
	// the caller has already enabled sds-node-configurator + sds-elastic
	// through other means).
	SkipModuleEnablement bool

	// SdsElasticSettings overrides `spec.settings` of the sds-elastic
	// ModuleConfig. Defaults to an empty map.
	SdsElasticSettings map[string]interface{}

	// --- Timeouts ---

	ModulesReadyTimeout     time.Duration // default 15m
	CephClusterReadyTimeout time.Duration // default 20m
	CephPoolReadyTimeout    time.Duration // default 10m
}

func (c *RookCephClusterConfig) applyDefaults() {
	if c.Namespace == "" {
		c.Namespace = kubernetes.DefaultRookNamespace
	}
	if c.CephClusterName == "" {
		c.CephClusterName = kubernetes.DefaultCephClusterName
	}
	if c.CephImage == "" {
		c.CephImage = kubernetes.DefaultCephImage
	}
	if c.MonCount <= 0 {
		c.MonCount = 1
	}
	if c.MgrCount <= 0 {
		c.MgrCount = 1
	}
	if c.OSDCount <= 0 {
		c.OSDCount = 1
	}
	if c.OSDSize == "" {
		c.OSDSize = kubernetes.DefaultOSDStorageClassSize
	}
	if c.OSDBackingStorageClassName == "" {
		c.OSDBackingStorageClassName = "sds-local-volume-thick-ceph-osd"
	}
	if c.OSDBackingLVMType == "" {
		c.OSDBackingLVMType = "Thick"
	}
	if c.ReplicaSize <= 0 {
		c.ReplicaSize = 1
	}
	if c.PoolName == "" {
		c.PoolName = fmt.Sprintf("ceph-rbd-r%d", c.ReplicaSize)
	}
	if c.FailureDomain == "" {
		if c.ReplicaSize == 1 {
			c.FailureDomain = "osd"
		} else {
			c.FailureDomain = "host"
		}
	}
	if c.ModulesReadyTimeout == 0 {
		c.ModulesReadyTimeout = 15 * time.Minute
	}
	if c.CephClusterReadyTimeout == 0 {
		c.CephClusterReadyTimeout = 20 * time.Minute
	}
	if c.CephPoolReadyTimeout == 0 {
		c.CephPoolReadyTimeout = 10 * time.Minute
	}
}

// EnsureCephCluster brings up (or reuses) a Rook-managed Ceph cluster plus
// a CephBlockPool via sds-elastic — without touching csi-ceph.
//
// Flow:
//  1. Enable Deckhouse modules: sds-node-configurator + sds-elastic.
//  2. Resolve an OSD backing StorageClass (re-using EnsureDefaultStorageClass
//     when none is pre-provided).
//  3. Seed `rook-config-override` with per-test global Ceph settings.
//  4. Create the Rook CephCluster and wait until it is Created.
//  5. Create the CephBlockPool and wait until it is Ready.
//
// Idempotent: re-running picks up existing resources. Returns the pool
// name (same one callers would reference as Ceph pool, e.g. for a
// subsequent `rbd create`/`CephStorageClass.rbd.pool`).
func EnsureCephCluster(ctx context.Context, kubeconfig *rest.Config, cfg RookCephClusterConfig) (string, error) {
	cfg.applyDefaults()

	logger.Step(1, "Enabling Deckhouse modules for Rook (sds-node-configurator, sds-elastic)")
	if !cfg.SkipModuleEnablement {
		if err := ensureRookModules(ctx, kubeconfig, cfg.SdsElasticSettings, cfg.ModulesReadyTimeout); err != nil {
			return "", fmt.Errorf("enable rook modules: %w", err)
		}
	}
	logger.StepComplete(1, "Modules enabled")

	logger.Step(2, "Resolving OSD backing StorageClass")
	osdSC := cfg.OSDStorageClass
	if osdSC == "" {
		local := DefaultStorageClassConfig{
			StorageClassName:     cfg.OSDBackingStorageClassName,
			LVMType:              cfg.OSDBackingLVMType,
			IncludeMasters:       cfg.OSDBackingIncludeMasters,
			BaseKubeconfig:       cfg.OSDBackingBaseKubeconfig,
			VMNamespace:          cfg.OSDBackingVMNamespace,
			BaseStorageClassName: cfg.OSDBackingBaseStorageClassName,
		}
		name, err := EnsureDefaultStorageClass(ctx, kubeconfig, local)
		if err != nil {
			return "", fmt.Errorf("resolve OSD backing StorageClass: %w", err)
		}
		osdSC = name
	} else {
		logger.Info("Using pre-existing OSD backing StorageClass %s", osdSC)
	}
	logger.StepComplete(2, "OSD backing StorageClass: %s", osdSC)

	logger.Step(3, "Seeding rook-config-override ConfigMap")
	if err := kubernetes.SetRookConfigOverride(ctx, kubeconfig, cfg.Namespace, cfg.GlobalCephConfigOverrides); err != nil {
		return "", fmt.Errorf("set rook-config-override: %w", err)
	}
	logger.StepComplete(3, "rook-config-override ready (%d global key(s))", len(cfg.GlobalCephConfigOverrides))

	logger.Step(4, "Creating Rook CephCluster %s/%s", cfg.Namespace, cfg.CephClusterName)
	if err := kubernetes.CreateCephCluster(ctx, kubeconfig, kubernetes.CephClusterConfig{
		Name:                cfg.CephClusterName,
		Namespace:           cfg.Namespace,
		CephImage:           cfg.CephImage,
		MonCount:            cfg.MonCount,
		MgrCount:            cfg.MgrCount,
		NetworkProvider:     cfg.NetworkProvider,
		PublicNetworkCIDRs:  cfg.PublicNetworkCIDRs,
		ClusterNetworkCIDRs: cfg.ClusterNetworkCIDRs,
		OSDStorageClass:     osdSC,
		OSDCount:            cfg.OSDCount,
		OSDSize:             cfg.OSDSize,
	}); err != nil {
		return "", fmt.Errorf("create CephCluster: %w", err)
	}
	if err := kubernetes.WaitForCephClusterReady(ctx, kubeconfig, cfg.Namespace, cfg.CephClusterName, cfg.CephClusterReadyTimeout); err != nil {
		return "", fmt.Errorf("wait CephCluster: %w", err)
	}
	logger.StepComplete(4, "CephCluster %s/%s is Created", cfg.Namespace, cfg.CephClusterName)

	logger.Step(5, "Creating CephBlockPool %s/%s (replica=%d, failureDomain=%s)",
		cfg.Namespace, cfg.PoolName, cfg.ReplicaSize, cfg.FailureDomain)
	if err := kubernetes.CreateCephBlockPool(ctx, kubeconfig, kubernetes.CephBlockPoolConfig{
		Name:          cfg.PoolName,
		Namespace:     cfg.Namespace,
		FailureDomain: cfg.FailureDomain,
		ReplicaSize:   cfg.ReplicaSize,
	}); err != nil {
		return "", fmt.Errorf("create CephBlockPool: %w", err)
	}
	if err := kubernetes.WaitForCephBlockPoolReady(ctx, kubeconfig, cfg.Namespace, cfg.PoolName, cfg.CephPoolReadyTimeout); err != nil {
		return "", fmt.Errorf("wait CephBlockPool: %w", err)
	}
	logger.StepComplete(5, "CephBlockPool %s/%s is Ready", cfg.Namespace, cfg.PoolName)

	logger.Success("Ceph cluster ready: CephCluster %s/%s + pool %s (no csi-ceph wiring)",
		cfg.Namespace, cfg.CephClusterName, cfg.PoolName)
	return cfg.PoolName, nil
}

// ensureRookModules enables sds-node-configurator + sds-elastic (and nothing
// else). Used by EnsureCephCluster and as the Rook-only step of
// EnsureCephStorageClass's module list.
func ensureRookModules(ctx context.Context, kubeconfig *rest.Config, sdsElasticSettings map[string]interface{}, readyTimeout time.Duration) error {
	if sdsElasticSettings == nil {
		sdsElasticSettings = map[string]interface{}{}
	}
	modules := []kubernetes.ModuleSpec{
		{
			Name:    "sds-node-configurator",
			Version: 1,
			Enabled: true,
		},
		{
			Name:         "sds-elastic",
			Version:      1,
			Enabled:      true,
			Settings:     sdsElasticSettings,
			Dependencies: []string{"sds-node-configurator"},
		},
	}
	return kubernetes.EnableModulesAndWait(ctx, kubeconfig, nil, nil, modules, readyTimeout)
}
