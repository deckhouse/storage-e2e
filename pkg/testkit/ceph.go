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

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// Re-exports of the supported CephStorageClass types so callers don't have
// to import the lower-level pkg/kubernetes package just to set cfg.Type.
const (
	CephStorageClassTypeRBD    = kubernetes.CephStorageClassTypeRBD
	CephStorageClassTypeCephFS = kubernetes.CephStorageClassTypeCephFS
)

// CephStorageClassConfig controls the end-to-end provisioning of a
// Rook-managed Ceph cluster plus a csi-ceph-backed k8s StorageClass:
//
//  1. Enables Deckhouse modules required for the stack:
//     sds-node-configurator, sds-elastic (Rook), csi-ceph.
//  2. (Optional) Falls back to EnsureDefaultStorageClass to produce a
//     sds-local-volume StorageClass for backing OSD PVCs.
//  3. Seeds `rook-config-override` with per-test global Ceph settings
//     (e.g. `ms_crc_data = false` for the PR #131 scenario).
//  4. Creates a CephCluster (Rook) and waits until it is Created.
//  5. Creates a CephBlockPool and waits until it is Ready.
//  6. Reads fsid / monitors / CephX admin key from Rook-managed secrets
//     and wires them into CephClusterConnection + CephClusterAuthentication
//     CRs so csi-ceph can talk to the cluster.
//  7. Creates a CephStorageClass CR and waits for the csi-ceph controller
//     to materialize a core storage.k8s.io/v1 StorageClass.
//
// Only StorageClassName is strictly required; everything else has sensible
// defaults tuned for single-node / tiny test clusters.
type CephStorageClassConfig struct {
	// --- Top-level identity ---

	// StorageClassName is the name of the CephStorageClass CR (and of the
	// resulting k8s StorageClass). Required.
	StorageClassName string

	// Namespace is the Rook / sds-elastic namespace. Default: "d8-sds-elastic".
	Namespace string

	// --- sds-elastic / Rook CephCluster ---

	// CephClusterName is the Rook CephCluster name. Default: "ceph-cluster".
	CephClusterName string

	// CephImage is the Ceph container image tag. Default: "quay.io/ceph/ceph:v18.2.7".
	CephImage string

	// MonCount / MgrCount are the Rook mon/mgr replica counts.
	// Defaults: 1 / 1 (good for 1..3 node test clusters).
	MonCount int
	MgrCount int

	// NetworkProvider: "" for CNI (default), "host" for host networking.
	NetworkProvider     string
	PublicNetworkCIDRs  []string
	ClusterNetworkCIDRs []string

	// GlobalCephConfigOverrides populates `rook-config-override` under
	// `[global]`, e.g. {"ms_crc_data": "false"}. nil / empty map leaves
	// the ConfigMap untouched except for creating it as an empty `[global]`.
	GlobalCephConfigOverrides map[string]string

	// --- OSD backing ---

	// OSDStorageClass is a block-capable StorageClass used to back OSD PVCs.
	// When empty, EnsureDefaultStorageClass is invoked with
	// OSDBackingStorageClass* to provision a sds-local-volume SC.
	OSDStorageClass string

	// OSDCount is the number of OSDs. Default: 1.
	OSDCount int

	// OSDSize is the size of each OSD PVC. Default: "10Gi".
	OSDSize string

	// --- Fallback SC provisioning via sds-local-volume (when OSDStorageClass is empty) ---

	// OSDBackingStorageClassName names the sds-local-volume SC that we
	// auto-provision for OSDs. Default: "sds-local-volume-thin-ceph-osd".
	OSDBackingStorageClassName string

	// OSDBackingLVMType passed to EnsureDefaultStorageClass ("Thick"/"Thin").
	// Default: "Thick" (simpler for block-mode PVCs used as Ceph OSDs).
	OSDBackingLVMType string

	// OSDBackingIncludeMasters exposes EnsureDefaultStorageClass.IncludeMasters.
	OSDBackingIncludeMasters bool

	// OSDBackingBaseKubeconfig/VMNamespace/BaseStorageClassName are plumbed
	// through to EnsureDefaultStorageClass to enable automatic VirtualDisk
	// attachment on nested-VM clusters.
	OSDBackingBaseKubeconfig       *rest.Config
	OSDBackingVMNamespace          string
	OSDBackingBaseStorageClassName string

	// MasterSSH is optional SSH access to the control plane. Not used by
	// EnsureCephStorageClass in this revision; callers may set it for
	// follow-up bootstrap or diagnostics hooks.
	MasterSSH ssh.SSHClient

	// --- CephBlockPool ---

	// PoolName is the Rook CephBlockPool name (also becomes the Ceph pool
	// name referenced by CephStorageClass.spec.rbd.pool).
	// Default: "ceph-rbd-r<ReplicaSize>".
	PoolName string

	// ReplicaSize is the CephBlockPool replication factor. Default: 1.
	ReplicaSize int

	// FailureDomain is the CRUSH failure domain: "host" or "osd".
	// Default: "osd" when ReplicaSize==1, "host" otherwise.
	FailureDomain string

	// --- Pool kind ---

	// Type selects the backing Ceph primitive: "RBD" (default) provisions a
	// CephBlockPool; "CephFS" provisions a CephFilesystem. The resulting
	// csi-ceph CephStorageClass CR mirrors this choice via spec.type.
	Type string

	// --- CephFilesystem (used only when Type == "CephFS") ---

	// CephFSName is the Rook CephFilesystem name. Default: "ceph-fs".
	CephFSName string

	// CephFSDataPoolName is the per-filesystem data pool name (Rook-side,
	// not the full Ceph pool name). Default: "data0".
	CephFSDataPoolName string

	// CephFSMetadataReplicas is the metadata pool replication factor.
	// Default: ReplicaSize.
	CephFSMetadataReplicas int

	// CephFSDataReplicas is the data pool replication factor.
	// Default: ReplicaSize.
	CephFSDataReplicas int

	// CephFSActiveMDSCount is the number of active MDS daemons. Default: 1.
	CephFSActiveMDSCount int

	// --- csi-ceph wiring ---

	// ClusterConnectionName and ClusterAuthenticationName point at the
	// CephClusterConnection / CephClusterAuthentication CRs we create.
	// Defaults: both "<StorageClassName>-conn".
	ClusterConnectionName     string
	ClusterAuthenticationName string

	// RBDDefaultFSType picks the mkfs used on attach. Default: "ext4".
	RBDDefaultFSType string

	// --- Modules ---

	// SkipModuleEnablement disables the module-enable step (useful when the
	// caller has already configured ModuleConfig on the cluster).
	SkipModuleEnablement bool

	// SkipClusterTeardown leaves the underlying Rook CephCluster and the
	// rook-config-override ConfigMap in place during TeardownCephStorageClass.
	// Use it when several StorageClasses share a single CephCluster — the
	// "owning" call should leave the flag false and tear the cluster down
	// last, while every other teardown sets it to true and only removes its
	// SC-specific resources (CephStorageClass / connection / auth / pool /
	// filesystem).
	SkipClusterTeardown bool

	// SdsElasticSettings overrides `spec.settings` of the sds-elastic
	// ModuleConfig. Defaults to the minimal set that makes sense on a
	// single-node test cluster.
	SdsElasticSettings map[string]interface{}

	// CsiCephSettings overrides `spec.settings` of the csi-ceph ModuleConfig.
	CsiCephSettings map[string]interface{}

	// CsiCephModulePullOverride pins a specific csi-ceph image tag (dev
	// registry only). Useful for testing PRs that haven't been released yet.
	CsiCephModulePullOverride string

	// --- Timeouts ---

	ModulesReadyTimeout        time.Duration // default 15m
	CephClusterReadyTimeout    time.Duration // default 20m
	CephPoolReadyTimeout       time.Duration // default 10m
	CephFilesystemReadyTimeout time.Duration // default 10m
	CredentialsTimeout         time.Duration // default 10m
	CSICephPhaseTimeout        time.Duration // default 5m
	StorageClassWaitTimeout    time.Duration // default 2m
}

func (c *CephStorageClassConfig) applyDefaults() {
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
	if c.ClusterConnectionName == "" {
		c.ClusterConnectionName = c.StorageClassName + "-conn"
	}
	if c.ClusterAuthenticationName == "" {
		c.ClusterAuthenticationName = c.StorageClassName + "-conn"
	}
	if c.RBDDefaultFSType == "" {
		c.RBDDefaultFSType = "ext4"
	}
	if c.Type == "" {
		c.Type = kubernetes.CephStorageClassTypeRBD
	}
	if c.CephFSName == "" {
		c.CephFSName = "ceph-fs"
	}
	if c.CephFSDataPoolName == "" {
		c.CephFSDataPoolName = "data0"
	}
	if c.CephFSMetadataReplicas <= 0 {
		c.CephFSMetadataReplicas = c.ReplicaSize
	}
	if c.CephFSDataReplicas <= 0 {
		c.CephFSDataReplicas = c.ReplicaSize
	}
	if c.CephFSActiveMDSCount <= 0 {
		c.CephFSActiveMDSCount = 1
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
	if c.CephFilesystemReadyTimeout == 0 {
		c.CephFilesystemReadyTimeout = 10 * time.Minute
	}
	if c.CredentialsTimeout == 0 {
		c.CredentialsTimeout = 10 * time.Minute
	}
	if c.CSICephPhaseTimeout == 0 {
		c.CSICephPhaseTimeout = 5 * time.Minute
	}
	if c.StorageClassWaitTimeout == 0 {
		c.StorageClassWaitTimeout = 2 * time.Minute
	}
}

// EnsureCephStorageClass is the high-level entry point that turns an empty
// cluster into one with a working csi-ceph StorageClass. See
// CephStorageClassConfig for the step-by-step flow.
//
// The function is idempotent: re-running it picks up the existing Rook
// CephCluster / pool / csi-ceph CRs and only fills in whatever is still
// missing. Returns the name of the resulting k8s StorageClass.
func EnsureCephStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg CephStorageClassConfig) (string, error) {
	cfg.applyDefaults()

	if cfg.StorageClassName == "" {
		return "", fmt.Errorf("StorageClassName is required")
	}

	logger.Step(1, "Enabling Deckhouse modules for csi-ceph (sds-node-configurator, sds-elastic, csi-ceph)")
	if !cfg.SkipModuleEnablement {
		if err := ensureCephModules(ctx, kubeconfig, cfg); err != nil {
			return "", fmt.Errorf("enable ceph modules: %w", err)
		}
	}
	logger.StepComplete(1, "Modules enabled")

	logger.Step(2, "Resolving OSD backing StorageClass")
	osdSC, err := ensureOSDBackingStorageClass(ctx, kubeconfig, &cfg)
	if err != nil {
		return "", fmt.Errorf("resolve OSD backing StorageClass: %w", err)
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

	switch cfg.Type {
	case kubernetes.CephStorageClassTypeRBD:
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
	case kubernetes.CephStorageClassTypeCephFS:
		logger.Step(5, "Creating CephFilesystem %s/%s (metadata replica=%d, data pool %q replica=%d, failureDomain=%s, activeMDS=%d)",
			cfg.Namespace, cfg.CephFSName,
			cfg.CephFSMetadataReplicas, cfg.CephFSDataPoolName, cfg.CephFSDataReplicas,
			cfg.FailureDomain, cfg.CephFSActiveMDSCount)
		if err := kubernetes.CreateCephFilesystem(ctx, kubeconfig, kubernetes.CephFilesystemConfig{
			Name:                      cfg.CephFSName,
			Namespace:                 cfg.Namespace,
			FailureDomain:             cfg.FailureDomain,
			MetadataPoolReplicas:      cfg.CephFSMetadataReplicas,
			DataPoolName:              cfg.CephFSDataPoolName,
			DataPoolReplicas:          cfg.CephFSDataReplicas,
			MetadataServerActiveCount: cfg.CephFSActiveMDSCount,
		}); err != nil {
			return "", fmt.Errorf("create CephFilesystem: %w", err)
		}
		if err := kubernetes.WaitForCephFilesystemReady(ctx, kubeconfig, cfg.Namespace, cfg.CephFSName, cfg.CephFilesystemReadyTimeout); err != nil {
			return "", fmt.Errorf("wait CephFilesystem: %w", err)
		}
		logger.StepComplete(5, "CephFilesystem %s/%s is Ready", cfg.Namespace, cfg.CephFSName)
	default:
		return "", fmt.Errorf("unsupported CephStorageClass Type: %s", cfg.Type)
	}

	logger.Step(6, "Extracting Rook-managed Ceph credentials (fsid, monitors, admin key)")
	creds, err := kubernetes.WaitForCephCredentials(ctx, kubeconfig, cfg.Namespace, cfg.CredentialsTimeout)
	if err != nil {
		return "", fmt.Errorf("wait ceph credentials: %w", err)
	}
	logger.StepComplete(6, "Ceph credentials: fsid=%s, user=%s, %d monitor(s): %v",
		creds.FSID, creds.AdminUser, len(creds.Monitors), creds.Monitors)

	logger.Step(7, "Wiring csi-ceph: CephClusterAuthentication %q + CephClusterConnection %q",
		cfg.ClusterAuthenticationName, cfg.ClusterConnectionName)
	if err := kubernetes.CreateCephClusterAuthentication(ctx, kubeconfig, kubernetes.CephClusterAuthenticationConfig{
		Name:    cfg.ClusterAuthenticationName,
		UserID:  creds.AdminUser,
		UserKey: creds.AdminKey,
	}); err != nil {
		return "", fmt.Errorf("create CephClusterAuthentication: %w", err)
	}
	if err := kubernetes.CreateCephClusterConnection(ctx, kubeconfig, kubernetes.CephClusterConnectionConfig{
		Name:      cfg.ClusterConnectionName,
		ClusterID: creds.FSID,
		Monitors:  creds.Monitors,
		UserID:    creds.AdminUser,
		UserKey:   creds.AdminKey,
	}); err != nil {
		return "", fmt.Errorf("create CephClusterConnection: %w", err)
	}
	if err := kubernetes.WaitForCephClusterConnectionCreated(ctx, kubeconfig, cfg.ClusterConnectionName, cfg.CSICephPhaseTimeout); err != nil {
		return "", fmt.Errorf("wait CephClusterConnection: %w", err)
	}
	logger.StepComplete(7, "csi-ceph wired against Ceph cluster %s", creds.FSID)

	logger.Step(8, "Creating CephStorageClass %q (type=%s) → StorageClass", cfg.StorageClassName, cfg.Type)
	cscCfg := kubernetes.CephStorageClassConfig{
		Name:                      cfg.StorageClassName,
		ClusterConnectionName:     cfg.ClusterConnectionName,
		ClusterAuthenticationName: cfg.ClusterAuthenticationName,
		Type:                      cfg.Type,
	}
	switch cfg.Type {
	case kubernetes.CephStorageClassTypeRBD:
		cscCfg.RBDPool = cfg.PoolName
		cscCfg.RBDDefaultFSType = cfg.RBDDefaultFSType
	case kubernetes.CephStorageClassTypeCephFS:
		cscCfg.CephFSName = cfg.CephFSName
		cscCfg.CephFSPool = kubernetes.CephFSDataPoolFullName(cfg.CephFSName, cfg.CephFSDataPoolName)
	default:
		return "", fmt.Errorf("unsupported CephStorageClass Type: %s", cfg.Type)
	}
	if err := kubernetes.CreateCephStorageClass(ctx, kubeconfig, cscCfg); err != nil {
		return "", fmt.Errorf("create CephStorageClass: %w", err)
	}
	if err := kubernetes.WaitForCephStorageClassCreated(ctx, kubeconfig, cfg.StorageClassName, cfg.CSICephPhaseTimeout); err != nil {
		return "", fmt.Errorf("wait CephStorageClass: %w", err)
	}
	if err := kubernetes.WaitForStorageClass(ctx, kubeconfig, cfg.StorageClassName, cfg.StorageClassWaitTimeout); err != nil {
		return "", fmt.Errorf("wait core StorageClass: %w", err)
	}
	logger.StepComplete(8, "StorageClass %s is available", cfg.StorageClassName)

	switch cfg.Type {
	case kubernetes.CephStorageClassTypeCephFS:
		logger.Success("Ceph e2e stack ready: CephCluster %s/%s + filesystem %s → StorageClass %s",
			cfg.Namespace, cfg.CephClusterName, cfg.CephFSName, cfg.StorageClassName)
	default:
		logger.Success("Ceph e2e stack ready: CephCluster %s/%s + pool %s → StorageClass %s",
			cfg.Namespace, cfg.CephClusterName, cfg.PoolName, cfg.StorageClassName)
	}
	return cfg.StorageClassName, nil
}

// TeardownCephStorageClass removes the csi-ceph wiring + Rook CephCluster +
// pool + rook-config-override produced by EnsureCephStorageClass. Safe to
// call on partial state (missing resources are skipped — the first error is
// returned but subsequent deletions are still attempted).
//
// It deliberately does NOT disable the Deckhouse modules: they may be owned
// by the cluster admin, and re-bootstrapping is cheaper than a full
// module-disable → module-enable cycle.
func TeardownCephStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg CephStorageClassConfig) error {
	cfg.applyDefaults()

	var firstErr error
	note := func(err error, what string) {
		if err == nil {
			return
		}
		logger.Warn("teardown: %s: %v", what, err)
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", what, err)
		}
	}

	logger.Info("Tearing down csi-ceph StorageClass %q (type=%s)", cfg.StorageClassName, cfg.Type)
	note(kubernetes.DeleteCephStorageClass(ctx, kubeconfig, cfg.StorageClassName), "delete CephStorageClass")
	note(kubernetes.DeleteCephClusterConnection(ctx, kubeconfig, cfg.ClusterConnectionName), "delete CephClusterConnection")
	note(kubernetes.DeleteCephClusterAuthentication(ctx, kubeconfig, cfg.ClusterAuthenticationName), "delete CephClusterAuthentication")
	switch cfg.Type {
	case kubernetes.CephStorageClassTypeCephFS:
		note(kubernetes.DeleteCephFilesystem(ctx, kubeconfig, cfg.Namespace, cfg.CephFSName), "delete CephFilesystem")
	default:
		note(kubernetes.DeleteCephBlockPool(ctx, kubeconfig, cfg.Namespace, cfg.PoolName), "delete CephBlockPool")
	}
	if !cfg.SkipClusterTeardown {
		note(kubernetes.DeleteCephCluster(ctx, kubeconfig, cfg.Namespace, cfg.CephClusterName), "delete CephCluster")
		note(kubernetes.DeleteRookConfigOverride(ctx, kubeconfig, cfg.Namespace), "delete rook-config-override")
	} else {
		logger.Info("Skipping CephCluster + rook-config-override teardown (SkipClusterTeardown=true)")
	}
	return firstErr
}

// EnsureDefaultCephStorageClass is EnsureCephStorageClass + SetGlobalDefaultStorageClass.
// After this call new PVCs without an explicit storageClassName will use the
// freshly-provisioned Ceph RBD class.
func EnsureDefaultCephStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg CephStorageClassConfig) (string, error) {
	scName, err := EnsureCephStorageClass(ctx, kubeconfig, cfg)
	if err != nil {
		return "", err
	}
	if err := kubernetes.SetGlobalDefaultStorageClass(ctx, kubeconfig, scName); err != nil {
		return "", fmt.Errorf("set %s as default in global ModuleConfig: %w", scName, err)
	}
	logger.Success("StorageClass %s set as cluster default", scName)
	return scName, nil
}

// ensureCephModules enables sds-node-configurator + sds-elastic + csi-ceph
// and waits for their Ready phase.
func ensureCephModules(ctx context.Context, kubeconfig *rest.Config, cfg CephStorageClassConfig) error {
	sdsElasticSettings := cfg.SdsElasticSettings
	if sdsElasticSettings == nil {
		sdsElasticSettings = map[string]interface{}{}
	}

	csiCephSettings := cfg.CsiCephSettings
	if csiCephSettings == nil {
		csiCephSettings = map[string]interface{}{}
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
		{
			Name:               "csi-ceph",
			Version:            1,
			Enabled:            true,
			Settings:           csiCephSettings,
			Dependencies:       []string{"sds-elastic"},
			ModulePullOverride: cfg.CsiCephModulePullOverride,
		},
	}
	return kubernetes.EnableModulesAndWait(ctx, kubeconfig, nil, nil, modules, cfg.ModulesReadyTimeout)
}

// ensureOSDBackingStorageClass returns an already-existing SC name (if the
// caller supplied OSDStorageClass) or delegates to EnsureDefaultStorageClass
// to provision a sds-local-volume SC on the fly.
func ensureOSDBackingStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg *CephStorageClassConfig) (string, error) {
	if cfg.OSDStorageClass != "" {
		logger.Info("Using pre-existing OSD backing StorageClass %s", cfg.OSDStorageClass)
		return cfg.OSDStorageClass, nil
	}

	localCfg := DefaultStorageClassConfig{
		StorageClassName:     cfg.OSDBackingStorageClassName,
		LVMType:              cfg.OSDBackingLVMType,
		IncludeMasters:       cfg.OSDBackingIncludeMasters,
		BaseKubeconfig:       cfg.OSDBackingBaseKubeconfig,
		VMNamespace:          cfg.OSDBackingVMNamespace,
		BaseStorageClassName: cfg.OSDBackingBaseStorageClassName,
	}
	return EnsureDefaultStorageClass(ctx, kubeconfig, localCfg)
}
