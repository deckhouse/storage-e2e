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
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const (
	maxVGStressDiskPrefix   = "stress-vg-d"
	maxVGStressLVGPrefix    = "stress-lvg-"
	maxVGStressVGNamePrefix = "stress-vg-"

	defaultMaxVGTarget   = 30
	defaultMaxVGBatch    = 5
	defaultMaxVGDiskSize = "1Gi"

	envMaxVGTarget   = "STRESS_MAX_VG_TARGET"
	envMaxVGBatch    = "STRESS_MAX_VG_BATCH_SIZE"
	envMaxVGDiskSize = "STRESS_MAX_VG_DISK_SIZE"
	envMaxVGStrict   = "STRESS_MAX_VG_STRICT"
	envMaxVGMinReady = "STRESS_MAX_VG_MIN_READY"

	lvgReadyTimeoutMin = 15 * time.Minute
	virtualDiskAttach  = 15 * time.Minute
	attachMaxRetries   = 3
	attachRetryWait    = 1 * time.Minute
	bdWaitTimeout      = 10 * time.Minute
)

// MaxVGsStressConfig controls the ramp of independent LVMVolumeGroups (1 disk = 1 VG) on one node.
type MaxVGsStressConfig struct {
	Target    int
	BatchSize int
	DiskSize  string
	Strict    bool
	MinReady  int

	Namespace    string
	StorageClass string
	RunID        string
}

// MaxVGsStressSlotReport is one ramp slot after Run.
type MaxVGsStressSlotReport struct {
	Index    int
	DiskName string
	LVGName  string
	VGName   string
	BDName   string
	Ready    bool
}

// MaxVGsStressResult is returned by MaxVGsStressRunner.Run.
type MaxVGsStressResult struct {
	NodeName     string
	Target       int
	ReadyTotal   int
	BatchSize    int
	StoppedEarly bool
	Strict       bool
	Slots        []MaxVGsStressSlotReport
	Attachments  []*kubernetes.VirtualDiskAttachmentResult
}

// DefaultMaxVGsStressConfig reads tuning from STRESS_MAX_VG_* environment variables.
func DefaultMaxVGsStressConfig(namespace, storageClass, runID string) MaxVGsStressConfig {
	target := envIntPositive(envMaxVGTarget, defaultMaxVGTarget)
	batch := envIntPositive(envMaxVGBatch, defaultMaxVGBatch)
	if batch > target {
		batch = target
	}
	diskSize := strings.TrimSpace(os.Getenv(envMaxVGDiskSize))
	if diskSize == "" {
		diskSize = defaultMaxVGDiskSize
	}
	strict := envBool(envMaxVGStrict)
	minReady := 1
	if strict {
		minReady = target
	}
	if v := strings.TrimSpace(os.Getenv(envMaxVGMinReady)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minReady = n
		}
	}
	return MaxVGsStressConfig{
		Target: target, BatchSize: batch, DiskSize: diskSize, Strict: strict, MinReady: minReady,
		Namespace: namespace, StorageClass: storageClass, RunID: runID,
	}
}

// MaxVGsStressRunner ramps VirtualDisk → BlockDevice → LVMVolumeGroup on a single node.
type MaxVGsStressRunner struct {
	Cfg        MaxVGsStressConfig
	NestedKube *rest.Config
	BaseKube   *rest.Config
}

func batchReadyTimeout(batchLen int) time.Duration {
	if batchLen <= 0 {
		return lvgReadyTimeoutMin
	}
	t := time.Duration(batchLen) * 4 * time.Minute
	if t < lvgReadyTimeoutMin {
		return lvgReadyTimeoutMin
	}
	const maxT = 3 * time.Hour
	if t > maxT {
		return maxT
	}
	return t
}

// Run executes the stress ramp. Caller must call Cleanup with Result.Attachments and slot LVG names.
func (r *MaxVGsStressRunner) Run(ctx context.Context) (*MaxVGsStressResult, error) {
	if r.NestedKube == nil || r.BaseKube == nil {
		return nil, fmt.Errorf("nested and base kubeconfig are required")
	}
	if r.Cfg.StorageClass == "" {
		return nil, fmt.Errorf("storage class is required")
	}
	if r.Cfg.RunID == "" {
		r.Cfg.RunID = fmt.Sprintf("%d", time.Now().Unix())
	}

	vms, err := kubernetes.ListVirtualMachineNames(ctx, r.BaseKube, r.Cfg.Namespace)
	if err != nil {
		return nil, err
	}
	targetVM := ""
	for _, vm := range vms {
		if strings.HasPrefix(vm, "bootstrap-node-") {
			continue
		}
		targetVM = vm
		break
	}
	if targetVM == "" {
		return nil, fmt.Errorf("no VirtualMachine found in namespace %s", r.Cfg.Namespace)
	}

	type slot struct {
		index   int
		disk    string
		vgName  string
		lvgName string
		meta    string
		bdName  string
		att     *kubernetes.VirtualDiskAttachmentResult
		ready   bool
	}

	slots := make([]slot, 0, r.Cfg.Target)
	var attachments []*kubernetes.VirtualDiskAttachmentResult
	nodeName := ""
	nodeSafe := ""
	readyTotal := 0
	stoppedEarly := false
	batchNum := 0

	logger.Info("Max-VG stress ramp: target=%d batch=%d diskSize=%s VM=%q", r.Cfg.Target, r.Cfg.BatchSize, r.Cfg.DiskSize, targetVM)

	for batchStart := 0; batchStart < r.Cfg.Target && !stoppedEarly; batchStart += r.Cfg.BatchSize {
		batchEnd := batchStart + r.Cfg.BatchSize
		if batchEnd > r.Cfg.Target {
			batchEnd = r.Cfg.Target
		}
		curBatch := batchEnd - batchStart
		batchNum++
		logger.Info("Batch %d: slots [%d..%d) (%d)", batchNum, batchStart, batchEnd, curBatch)

		for i := batchStart; i < batchEnd; i++ {
			idx := i + 1
			slots = append(slots, slot{
				index:  idx,
				disk:   fmt.Sprintf("%s%d-%s", maxVGStressDiskPrefix, idx, r.Cfg.RunID),
				vgName: fmt.Sprintf("%s%d-%s", maxVGStressVGNamePrefix, idx, r.Cfg.RunID),
			})
		}

		for i := batchStart; i < batchEnd; i++ {
			att, err := attachVirtualDiskWithRetry(ctx, r.BaseKube, kubernetes.VirtualDiskAttachmentConfig{
				VMName: targetVM, Namespace: r.Cfg.Namespace, DiskName: slots[i].disk,
				DiskSize: r.Cfg.DiskSize, StorageClassName: r.Cfg.StorageClass,
			})
			if err != nil {
				return nil, fmt.Errorf("attach %s: %w", slots[i].disk, err)
			}
			slots[i].att = att
			attachments = append(attachments, att)
		}

		for i := batchStart; i < batchEnd; i++ {
			attachCtx, cancel := context.WithTimeout(ctx, virtualDiskAttach)
			err := kubernetes.WaitForVirtualDiskAttached(attachCtx, r.BaseKube, r.Cfg.Namespace, slots[i].att.AttachmentName, 10*time.Second)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("wait attach %s: %w", slots[i].att.AttachmentName, err)
			}
		}

		metaSeen := make(map[string]struct{})
		for i := batchStart; i < batchEnd; i++ {
			bd, err := kubernetes.WaitConsumableBlockDeviceForVirtualDisk(ctx, r.NestedKube, r.BaseKube,
				r.Cfg.Namespace, slots[i].att.DiskName, slots[i].att.AttachmentName, targetVM, bdWaitTimeout)
			if err != nil {
				return nil, err
			}
			slots[i].bdName = bd.Name
			if nodeName == "" {
				nodeName = bd.Status.NodeName
				nodeSafe = strings.ReplaceAll(strings.ReplaceAll(nodeName, ".", "-"), "_", "-")
			} else if bd.Status.NodeName != nodeName {
				return nil, fmt.Errorf("BlockDevice %s on node %q, expected %q", bd.Name, bd.Status.NodeName, nodeName)
			}
			meta := bd.Labels["kubernetes.io/metadata.name"]
			if meta == "" {
				meta = bd.Name
			}
			if _, dup := metaSeen[meta]; dup {
				return nil, fmt.Errorf("duplicate BlockDevice selector meta %q", meta)
			}
			metaSeen[meta] = struct{}{}
			slots[i].meta = meta
			slots[i].lvgName = fmt.Sprintf("%s%d-%s-%s", maxVGStressLVGPrefix, slots[i].index, r.Cfg.RunID, nodeSafe)
		}

		for i := batchStart; i < batchEnd; i++ {
			labels := map[string]string{
				"kubernetes.io/hostname":      nodeName,
				"kubernetes.io/metadata.name": slots[i].meta,
			}
			if err := kubernetes.CreateLVMVolumeGroupWithMatchLabels(ctx, r.NestedKube, slots[i].lvgName, nodeName, slots[i].vgName, labels); err != nil {
				return nil, fmt.Errorf("create LVMVolumeGroup %s: %w", slots[i].lvgName, err)
			}
		}

		batchNames := make([]string, curBatch)
		for i := batchStart; i < batchEnd; i++ {
			batchNames[i-batchStart] = slots[i].lvgName
		}
		if !kubernetes.WaitForLVMBatchReady(ctx, r.NestedKube, batchNames, batchReadyTimeout(curBatch)) {
			logger.Warn("Batch %d: not all LVMVolumeGroups Ready within %v — stopping ramp", batchNum, batchReadyTimeout(curBatch))
			stoppedEarly = true
			for i := batchStart; i < batchEnd; i++ {
				n, _ := kubernetes.CountReadyLVMVolumeGroups(ctx, r.NestedKube, []string{slots[i].lvgName})
				if n == 1 {
					slots[i].ready = true
					readyTotal++
				}
			}
			break
		}
		for i := batchStart; i < batchEnd; i++ {
			slots[i].ready = true
		}
		readyTotal += curBatch
		logger.Success("Batch %d OK: cumulative Ready=%d", batchNum, readyTotal)
	}

	report := make([]MaxVGsStressSlotReport, len(slots))
	for i := range slots {
		report[i] = MaxVGsStressSlotReport{
			Index: slots[i].index, DiskName: slots[i].disk, LVGName: slots[i].lvgName,
			VGName: slots[i].vgName, BDName: slots[i].bdName, Ready: slots[i].ready,
		}
	}

	logger.Info("Max-VG stress finished: %d/%d Ready on node %s (stopped early=%v)", readyTotal, r.Cfg.Target, nodeName, stoppedEarly)

	return &MaxVGsStressResult{
		NodeName: nodeName, Target: r.Cfg.Target, ReadyTotal: readyTotal, BatchSize: r.Cfg.BatchSize,
		StoppedEarly: stoppedEarly, Strict: r.Cfg.Strict, Slots: report, Attachments: attachments,
	}, nil
}

// Cleanup detaches stress VirtualDisks and deletes stress LVMVolumeGroup CRs.
func CleanupMaxVGsStress(ctx context.Context, nestedKube, baseKube *rest.Config, namespace string, res *MaxVGsStressResult) {
	if res == nil {
		return
	}
	for _, att := range res.Attachments {
		if att == nil {
			continue
		}
		_ = kubernetes.DetachAndDeleteVirtualDisk(ctx, baseKube, namespace, att.AttachmentName, att.DiskName)
	}
	_ = kubernetes.DeleteLVMVolumeGroupsWithPrefix(ctx, nestedKube, maxVGStressLVGPrefix)
}

func attachVirtualDiskWithRetry(ctx context.Context, kube *rest.Config, cfg kubernetes.VirtualDiskAttachmentConfig) (*kubernetes.VirtualDiskAttachmentResult, error) {
	var lastErr error
	for attempt := 1; attempt <= attachMaxRetries; attempt++ {
		att, err := kubernetes.AttachVirtualDiskToVM(ctx, kube, cfg)
		if err == nil {
			return att, nil
		}
		lastErr = err
		if attempt < attachMaxRetries {
			logger.Warn("attach %s attempt %d/%d: %v; retry in %v", cfg.DiskName, attempt, attachMaxRetries, err, attachRetryWait)
			time.Sleep(attachRetryWait)
		}
	}
	return nil, lastErr
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envIntPositive(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
