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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/apps"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/core"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/storage"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// TestMode represents the mode of stress test
type TestMode string

const (
	ModeFlog                       TestMode = "flog"
	ModeCheckFSOnly                TestMode = "check_fs_only"
	ModeCheckCloning               TestMode = "check_cloning"
	ModeCheckRestoringFromSnapshot TestMode = "check_restoring_from_snapshot"
	ModeSnapshotResizeCloning      TestMode = "snapshot_resize_cloning"
)

// ResourceType represents the type of Kubernetes resource to create
type ResourceType string

const (
	ResourceTypePod         ResourceType = "pod"
	ResourceTypeDeployment  ResourceType = "deployment"
	ResourceTypeStatefulSet ResourceType = "statefulset"
)

// TestStep represents a step in snapshot_resize_cloning mode
type TestStep string

const (
	StepRestoreFromSnapshot TestStep = "restore_from_snapshot"
	StepResize              TestStep = "resize"
	StepClone               TestStep = "clone"
)

// Config holds the configuration for stress tests
type Config struct {
	// Basic configuration
	Namespace        string
	StorageClassName string
	PVCSize          string
	PodsCount        int
	ParallelismCount int
	SchedulerName    string
	ResourceType     ResourceType
	Mode             TestMode

	// Resize configuration
	PVCSizeAfterResize       string
	PVCSizeAfterResizeStage2 string

	// Snapshot configuration
	SnapshotsPerPVC int
	SnapshotName    string // For check_restoring_from_snapshot mode
	PVCForCloning   string // For check_cloning mode

	// Test order for snapshot_resize_cloning mode
	TestOrder []TestStep

	// Timeouts and retries
	MaxAttempts int
	Interval    time.Duration

	// Cleanup
	Cleanup         bool
	DeleteNamespace bool
}

// DefaultConfig returns a config with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		SchedulerName:   "default-scheduler",
		ResourceType:    ResourceTypePod,
		Mode:            ModeFlog,
		SnapshotsPerPVC: 1,
		MaxAttempts:     0, // 0 means infinite
		Interval:        5 * time.Second,
		Cleanup:         false,
		DeleteNamespace: false,
		TestOrder:       []TestStep{StepRestoreFromSnapshot, StepResize, StepClone},
	}
}

// StressTestRunner runs stress tests
type StressTestRunner struct {
	config          *Config
	namespaceClient *core.NamespaceClient
	pvcClient       *storage.PVCClient
	snapshotClient  *storage.VolumeSnapshotClient
	podClient       *core.PodClient
	deployClient    *apps.DeploymentClient
	restConfig      *rest.Config
}

// NewStressTestRunner creates a new stress test runner
func NewStressTestRunner(config *Config, restConfig *rest.Config) (*StressTestRunner, error) {
	namespaceClient, err := core.NewNamespaceClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace client: %w", err)
	}

	pvcClient, err := storage.NewPVCClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create PVC client: %w", err)
	}

	snapshotClient, err := storage.NewVolumeSnapshotClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create VolumeSnapshot client: %w", err)
	}

	podClient, err := core.NewPodClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod client: %w", err)
	}

	deployClient, err := apps.NewDeploymentClient(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment client: %w", err)
	}

	return &StressTestRunner{
		config:          config,
		namespaceClient: namespaceClient,
		pvcClient:       pvcClient,
		snapshotClient:  snapshotClient,
		podClient:       podClient,
		deployClient:    deployClient,
		restConfig:      restConfig,
	}, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if c.StorageClassName == "" {
		return fmt.Errorf("storage class name is required")
	}
	if c.PVCSize == "" {
		return fmt.Errorf("PVC size is required")
	}
	if c.PodsCount <= 0 {
		return fmt.Errorf("pods count must be > 0")
	}
	if c.ParallelismCount <= 0 {
		return fmt.Errorf("parallelism count must be > 0")
	}
	if c.ParallelismCount > c.PodsCount {
		return fmt.Errorf("parallelism count (%d) > pods count (%d)", c.ParallelismCount, c.PodsCount)
	}

	switch c.Mode {
	case ModeCheckCloning:
		if c.PVCForCloning == "" {
			return fmt.Errorf("PVC for cloning is required for check_cloning mode")
		}
		if c.ResourceType != ResourceTypePod {
			return fmt.Errorf("check_cloning mode only supports pod resource type")
		}
	case ModeCheckRestoringFromSnapshot:
		if c.SnapshotName == "" {
			return fmt.Errorf("snapshot name is required for check_restoring_from_snapshot mode")
		}
		if c.ResourceType != ResourceTypePod {
			return fmt.Errorf("check_restoring_from_snapshot mode only supports pod resource type")
		}
	case ModeSnapshotResizeCloning:
		if c.ResourceType != ResourceTypePod {
			return fmt.Errorf("snapshot_resize_cloning mode only supports pod resource type")
		}
		if c.SnapshotsPerPVC <= 0 {
			return fmt.Errorf("snapshots per PVC must be > 0")
		}
		// Validate test order
		for _, step := range c.TestOrder {
			switch step {
			case StepRestoreFromSnapshot, StepResize, StepClone:
				// Valid steps
			default:
				return fmt.Errorf("invalid test step: %s (allowed: restore_from_snapshot, resize, clone)", step)
			}
		}
		// Check required parameters for steps
		hasResize := false
		hasCloneOrRestore := false
		for _, step := range c.TestOrder {
			if step == StepResize {
				hasResize = true
			}
			if step == StepClone || step == StepRestoreFromSnapshot {
				hasCloneOrRestore = true
			}
		}
		if hasResize && c.PVCSizeAfterResize == "" {
			return fmt.Errorf("PVC size after resize is required when resize step is enabled")
		}
		if hasCloneOrRestore && c.PVCSizeAfterResizeStage2 == "" {
			return fmt.Errorf("PVC size after resize stage2 is required when clone/restore steps are enabled")
		}
	}

	return nil
}

// Run executes the stress test
func (r *StressTestRunner) Run(ctx context.Context) error {
	if err := r.config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Ensure namespace exists
	_, err := r.namespaceClient.Get(ctx, r.config.Namespace)
	if err != nil {
		_, err = r.namespaceClient.Create(ctx, r.config.Namespace)
		if err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
		logger.Debug("Created namespace %s", r.config.Namespace)
	}

	// Label namespace
	err = r.namespaceClient.Patch(ctx, r.config.Namespace, types.JSONPatchType, []byte(`[{"op": "add", "path": "/metadata/labels/load-test", "value": "true"}]`))
	if err != nil {
		// Ignore if label already exists
		logger.Debug("Failed to patch namespace label (may already exist): %v", err)
	}

	switch r.config.Mode {
	case ModeFlog:
		return r.runFlogMode(ctx)
	case ModeCheckFSOnly:
		return r.runCheckFSOnlyMode(ctx)
	case ModeCheckCloning:
		return r.runCheckCloningMode(ctx)
	case ModeCheckRestoringFromSnapshot:
		return r.runCheckRestoringFromSnapshotMode(ctx)
	case ModeSnapshotResizeCloning:
		return r.runSnapshotResizeCloningMode(ctx)
	default:
		return fmt.Errorf("unknown mode: %s", r.config.Mode)
	}
}

// createOriginalPodAndPVC creates a pod and PVC for snapshot_resize_cloning mode
func (r *StressTestRunner) createOriginalPodAndPVC(ctx context.Context, index int) error {
	pvcName := fmt.Sprintf("pvc-test-%d", index)
	podName := fmt.Sprintf("pod-test-%d", index)

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "original",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(r.config.PVCSize),
				},
			},
			StorageClassName: &r.config.StorageClassName,
			VolumeMode:       func() *corev1.PersistentVolumeMode { v := corev1.PersistentVolumeFilesystem; return &v }(),
		},
	}

	_, err := r.pvcClient.Create(ctx, r.config.Namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
	}
	logger.Debug("Created PVC %s/%s", r.config.Namespace, pvcName)

	// Create Pod with data preloader and writer
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "original",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SchedulerName: r.config.SchedulerName,
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
						{
							Weight: 100,
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "load-test",
											Operator: metav1.LabelSelectorOpIn,
											Values:   []string{"true"},
										},
									},
								},
								TopologyKey: "kubernetes.io/hostname",
							},
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			InitContainers: []corev1.Container{
				{
					Name:    "data-preloader",
					Image:   "alpine",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`set -e
dir="/usr/share/test-data"
mkdir -p "$dir"
echo "Preloading 5 files..."
for i in 1 2 3 4 5; do
  fname="preload_file_${i}"
  blocks=$(( ($RANDOM % 5120) + 1 ))
  dd if=/dev/urandom of="${dir}/${fname}" bs=1024 count=${blocks} conv=fsync status=none || exit 1
  tmp_sum="${dir}/${fname}.sha256.tmp"
  sha256sum "${dir}/${fname}" > "${tmp_sum}" || exit 1
  sync "${tmp_sum}" || true
  mv "${tmp_sum}" "${dir}/${fname}.sha256"
  echo "Created ${fname} (${blocks}KB)"
done`,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-volume",
							MountPath: "/usr/share/test-data",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "data-writer",
					Image:   "alpine",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`set -e
dir="/usr/share/test-data"
for sumf in "$dir"/*.sha256; do
  [ -e "$sumf" ] || continue
  case "$sumf" in *.tmp) continue ;; esac
  [ -s "$sumf" ] || continue
  sha256sum -c "$sumf"
done
echo "Data check passed"
trap 'exit 0' TERM INT
while true; do
  blocks=$(( ($RANDOM % 5120) + 1 ))
  fname="file_${RANDOM}_$(date +%s%N)"
  if dd if=/dev/urandom of="${dir}/${fname}" bs=1024 count=${blocks} conv=fsync status=none 2>/dev/null; then
    tmp_sum="${dir}/${fname}.sha256.tmp"
    if sha256sum "${dir}/${fname}" > "${tmp_sum}" 2>/dev/null; then
      sync "${tmp_sum}" 2>/dev/null || true
      if [ -s "${tmp_sum}" ]; then
        mv "${tmp_sum}" "${dir}/${fname}.sha256"
      else
        rm -f "${tmp_sum}" 2>/dev/null || true
      fi
    else
      rm -f "${tmp_sum}" 2>/dev/null || true
    fi
  fi
  sleep 1
done`,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-volume",
							MountPath: "/usr/share/test-data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-volume",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	_, err = r.podClient.Create(ctx, r.config.Namespace, pod)
	if err != nil {
		return fmt.Errorf("failed to create pod %s: %w", podName, err)
	}

	logger.Debug("Created pod %s/%s", r.config.Namespace, podName)
	return nil
}

// createFlogPodAndPVC creates a pod and PVC for flog mode
func (r *StressTestRunner) createFlogPodAndPVC(ctx context.Context, index int, firstStart bool) error {
	pvcName := fmt.Sprintf("pvc-test-%d", index)
	podName := fmt.Sprintf("pod-test-%d", index)

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
			Labels: map[string]string{
				"load-test": "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(r.config.PVCSize),
				},
			},
			StorageClassName: &r.config.StorageClassName,
			VolumeMode:       func() *corev1.PersistentVolumeMode { v := corev1.PersistentVolumeFilesystem; return &v }(),
		},
	}

	_, err := r.pvcClient.Create(ctx, r.config.Namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
	}
	logger.Debug("Created PVC %s/%s", r.config.Namespace, pvcName)

	firstStartStr := "false"
	if firstStart {
		firstStartStr = "true"
	}

	// Create Pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"load-test": "true",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SchedulerName: r.config.SchedulerName,
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
						{
							Weight: 100,
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "load-test",
											Operator: metav1.LabelSelectorOpIn,
											Values:   []string{"true"},
										},
									},
								},
								TopologyKey: "kubernetes.io/hostname",
							},
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "flog-generator",
					Image: "ex42zav/flog:0.4.3",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("20Mi"),
						},
					},
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`echo "Starting flog generator..."
ls -A /var/log/flog
folder_files=$(ls -A /var/log/flog 2>/dev/null | grep -v '^lost+found$')
echo "folder_files: $folder_files"
echo "FIRST_START: $FIRST_START"

if [ -n "$folder_files" ] && [ "$FIRST_START" = "true" ]; then
  echo "Error: leftover files found in /var/log/flog" >&2
  exit 1
fi

trap 'echo "Termination signal received, exiting..."; exit 0' TERM INT

while true; do
  /srv/flog/flog -b "${FLOG_BATCH_SIZE}" -f "${FLOG_LOG_FORMAT}" 2>&1 | tee -a /var/log/flog/fake.log
  if ! touch /var/log/flog/fake.log; then
    echo "Error: Unable to write to /var/log/flog/fake.log" >&2
    exit 1
  fi
  sleep ${FLOG_TIME_INTERVAL}
done`,
					},
					Env: []corev1.EnvVar{
						{Name: "FLOG_BATCH_SIZE", Value: "10700"},
						{Name: "FLOG_TIME_INTERVAL", Value: "1"},
						{Name: "FLOG_LOG_FORMAT", Value: "json"},
						{Name: "FIRST_START", Value: firstStartStr},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nginx-persistent-storage",
							MountPath: "/var/log/flog",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "nginx-persistent-storage",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	_, err = r.podClient.Create(ctx, r.config.Namespace, pod)
	if err != nil {
		return fmt.Errorf("failed to create pod %s: %w", podName, err)
	}

	logger.Debug("Created pod %s/%s", r.config.Namespace, podName)
	return nil
}

// waitForPodsStatus waits for pods to reach a specific status
func (r *StressTestRunner) waitForPodsStatus(ctx context.Context, labelSelector, status string, expectedCount int) error {
	attempt := 0
	lastLogTime := time.Now()

	for {
		// Check context cancellation first
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pods status %s: %w", status, ctx.Err())
		default:
		}

		pods, err := r.podClient.ListByLabelSelector(ctx, r.config.Namespace, labelSelector)
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		readyCount := 0
		failedPods := []string{}
		pendingPods := []string{}

		for _, pod := range pods.Items {
			podPhase := string(pod.Status.Phase)
			if podPhase == status || (status == "Completed" && pod.Status.Phase == corev1.PodSucceeded) {
				readyCount++
			} else if podPhase == "Failed" || podPhase == "Error" {
				failedPods = append(failedPods, fmt.Sprintf("%s (reason: %s)", pod.Name, pod.Status.Reason))
			} else if podPhase == "Pending" {
				pendingPods = append(pendingPods, pod.Name)
			}
		}

		// Log progress every 30 seconds
		if time.Since(lastLogTime) >= 30*time.Second {
			logger.Progress("Waiting for pods: %d/%d in status %s (attempt %d)", readyCount, expectedCount, status, attempt)
			if len(failedPods) > 0 {
				logger.Warn("Failed pods: %v", failedPods)
			}
			if len(pendingPods) > 0 && len(pendingPods) <= 10 {
				logger.Debug("Pending pods: %v", pendingPods)
			} else if len(pendingPods) > 10 {
				logger.Debug("Pending pods: %d pods still pending", len(pendingPods))
			}
			lastLogTime = time.Now()
		}

		if readyCount >= expectedCount {
			return nil
		}

		// Check for failures - if too many pods failed, return error early
		if len(failedPods) > 0 && len(failedPods) > expectedCount/10 {
			return fmt.Errorf("too many pods failed (%d failed): %v", len(failedPods), failedPods)
		}

		attempt++

		if r.config.MaxAttempts > 0 && attempt >= r.config.MaxAttempts {
			return fmt.Errorf("timeout waiting for pods status %s: %d/%d ready after %d attempts (failed: %d, pending: %d)",
				status, readyCount, expectedCount, r.config.MaxAttempts, len(failedPods), len(pendingPods))
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for pods status %s: %w", status, ctx.Err())
		case <-time.After(r.config.Interval):
		}
	}
}

// runFlogMode runs the flog mode test
func (r *StressTestRunner) runFlogMode(ctx context.Context) error {
	iterations := (r.config.PodsCount + r.config.ParallelismCount - 1) / r.config.ParallelismCount

	for i := 0; i < iterations; i++ {
		start := i*r.config.ParallelismCount + 1
		end := start + r.config.ParallelismCount - 1
		if end > r.config.PodsCount {
			end = r.config.PodsCount
		}

		for j := start; j <= end; j++ {
			firstStart := (i == 0 && j == start)
			if err := r.createFlogPodAndPVC(ctx, j, firstStart); err != nil {
				return err
			}
		}
	}

	// Wait for PVCs to be bound
	if err := r.pvcClient.WaitForBound(ctx, r.config.Namespace, "load-test=true", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to be running
	if err := r.waitForPodsStatus(ctx, "load-test=true", "Running", r.config.PodsCount); err != nil {
		return err
	}

	// Resize if configured
	if r.config.PVCSizeAfterResize != "" {
		pvcNames := make([]string, r.config.PodsCount)
		for i := 1; i <= r.config.PodsCount; i++ {
			pvcNames[i-1] = fmt.Sprintf("pvc-test-%d", i)
		}
		if err := r.pvcClient.ResizeList(ctx, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResize); err != nil {
			return err
		}
		if err := r.pvcClient.WaitForResize(ctx, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResize, r.config.MaxAttempts, r.config.Interval); err != nil {
			return err
		}
	}

	// Cleanup if requested
	if r.config.Cleanup {
		return r.cleanup(ctx)
	}

	return nil
}

// runCheckFSOnlyMode runs the check_fs_only mode test
func (r *StressTestRunner) runCheckFSOnlyMode(ctx context.Context) error {
	// Similar to flog mode but with different pod spec
	// Implementation would be similar to flog mode but with nginx container checking filesystem
	return fmt.Errorf("check_fs_only mode not yet implemented")
}

// runCheckCloningMode runs the check_cloning mode test
func (r *StressTestRunner) runCheckCloningMode(ctx context.Context) error {
	// Create PVCs cloned from the specified PVC
	// Implementation would create PVCs with dataSource pointing to r.config.PVCForCloning
	return fmt.Errorf("check_cloning mode not yet implemented")
}

// runCheckRestoringFromSnapshotMode runs the check_restoring_from_snapshot mode test
func (r *StressTestRunner) runCheckRestoringFromSnapshotMode(ctx context.Context) error {
	// Create PVCs restored from the specified snapshot
	// Implementation would create PVCs with dataSource pointing to r.config.SnapshotName
	return fmt.Errorf("check_restoring_from_snapshot mode not yet implemented")
}

// runSnapshotResizeCloningMode runs the snapshot_resize_cloning mode test
func (r *StressTestRunner) runSnapshotResizeCloningMode(ctx context.Context) error {
	// Create original pods and PVCs
	iterations := (r.config.PodsCount + r.config.ParallelismCount - 1) / r.config.ParallelismCount

	for i := 0; i < iterations; i++ {
		start := i*r.config.ParallelismCount + 1
		end := start + r.config.ParallelismCount - 1
		if end > r.config.PodsCount {
			end = r.config.PodsCount
		}

		for j := start; j <= end; j++ {
			if err := r.createOriginalPodAndPVC(ctx, j); err != nil {
				return err
			}
		}
	}

	// Wait for PVCs to be bound
	if err := r.pvcClient.WaitForBound(ctx, r.config.Namespace, "load-test=true,load-test-role=original", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to be running
	if err := r.waitForPodsStatus(ctx, "load-test=true,load-test-role=original", "Running", r.config.PodsCount); err != nil {
		return err
	}

	time.Sleep(5 * time.Second)

	var clonePVCNames []string
	var restorePVCNames []string
	originalPVCNames := make([]string, r.config.PodsCount)
	for i := 1; i <= r.config.PodsCount; i++ {
		originalPVCNames[i-1] = fmt.Sprintf("pvc-test-%d", i)
	}

	// Execute test steps
	for _, step := range r.config.TestOrder {
		switch step {
		case StepRestoreFromSnapshot:
			if err := r.executeRestoreFromSnapshotStep(ctx, &restorePVCNames); err != nil {
				return err
			}
		case StepResize:
			if err := r.pvcClient.ResizeList(ctx, r.config.Namespace, originalPVCNames, r.config.PVCSizeAfterResize); err != nil {
				return err
			}
			if err := r.pvcClient.WaitForResize(ctx, r.config.Namespace, originalPVCNames, r.config.PVCSizeAfterResize, r.config.MaxAttempts, r.config.Interval); err != nil {
				return err
			}
		case StepClone:
			if err := r.executeCloneStep(ctx, &clonePVCNames); err != nil {
				return err
			}
		}
	}

	// Stage 2: flog pods and resize for clones/restored
	stage2PVCs := append(clonePVCNames, restorePVCNames...)
	if len(stage2PVCs) > 0 {
		if err := r.executeStage2(ctx, stage2PVCs); err != nil {
			return err
		}
	}

	// Cleanup if requested
	if r.config.Cleanup {
		return r.cleanup(ctx)
	}

	return nil
}

// executeRestoreFromSnapshotStep executes the restore from snapshot step
func (r *StressTestRunner) executeRestoreFromSnapshotStep(ctx context.Context, restorePVCNames *[]string) error {
	// Create snapshots
	for batchStart := 1; batchStart <= r.config.PodsCount; batchStart += r.config.ParallelismCount {
		batchEnd := batchStart + r.config.ParallelismCount - 1
		if batchEnd > r.config.PodsCount {
			batchEnd = r.config.PodsCount
		}

		for k := batchStart; k <= batchEnd; k++ {
			for s := 1; s <= r.config.SnapshotsPerPVC; s++ {
				snapshotName := fmt.Sprintf("snapshot-test-%d-%d", k, s)
				if err := r.createVolumeSnapshot(ctx, k, snapshotName); err != nil {
					return err
				}
			}
		}
		time.Sleep(5 * time.Second)
	}

	totalSnapshots := r.config.PodsCount * r.config.SnapshotsPerPVC
	if err := r.snapshotClient.WaitForReady(ctx, r.config.Namespace, "load-test-role=snapshot", totalSnapshots, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Create restore PVCs and pods
	for k := 1; k <= r.config.PodsCount; k++ {
		for s := 1; s <= r.config.SnapshotsPerPVC; s++ {
			snapshotName := fmt.Sprintf("snapshot-test-%d-%d", k, s)
			pvcName := fmt.Sprintf("pvc-test-%d-restore-%d", k, s)
			podName := fmt.Sprintf("pod-test-%d-restore-%d", k, s)
			if err := r.createRestorePodAndPVC(ctx, snapshotName, pvcName, podName); err != nil {
				return err
			}
			*restorePVCNames = append(*restorePVCNames, pvcName)
		}
	}

	totalRestore := r.config.PodsCount * r.config.SnapshotsPerPVC
	if err := r.pvcClient.WaitForBound(ctx, r.config.Namespace, "load-test=true,load-test-role=restore", totalRestore, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}
	if err := r.waitForPodsStatus(ctx, "load-test=true,load-test-role=restore", "Completed", totalRestore); err != nil {
		return err
	}

	return nil
}

// executeCloneStep executes the clone step
func (r *StressTestRunner) executeCloneStep(ctx context.Context, clonePVCNames *[]string) error {
	for k := 1; k <= r.config.PodsCount; k++ {
		// Get current size of original PVC
		pvc, err := r.pvcClient.Get(ctx, r.config.Namespace, fmt.Sprintf("pvc-test-%d", k))
		if err != nil {
			return err
		}
		currentSize := r.config.PVCSize
		if pvc.Status.Capacity != nil {
			if size, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
				currentSize = size.String()
			}
		}

		pvcName := fmt.Sprintf("pvc-test-%d-clone", k)
		podName := fmt.Sprintf("pod-test-%d-clone", k)
		if err := r.createClonePodAndPVC(ctx, k, pvcName, podName, currentSize); err != nil {
			return err
		}
		*clonePVCNames = append(*clonePVCNames, pvcName)
	}

	if err := r.pvcClient.WaitForBound(ctx, r.config.Namespace, "load-test=true,load-test-role=clone", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}
	if err := r.waitForPodsStatus(ctx, "load-test=true,load-test-role=clone", "Completed", r.config.PodsCount); err != nil {
		return err
	}

	return nil
}

// executeStage2 executes stage 2: flog pods and resize
func (r *StressTestRunner) executeStage2(ctx context.Context, pvcNames []string) error {
	// Create flog pods for each PVC
	for _, pvcName := range pvcNames {
		podName := fmt.Sprintf("%s-flog", pvcName)
		role := "clone-flog"
		if len(pvcName) > 8 && pvcName[len(pvcName)-8:] == "-restore" {
			role = "restore-flog"
		}
		if err := r.createFlogPodForPVC(ctx, podName, pvcName, role); err != nil {
			return err
		}
	}

	// Wait for pods to be running
	cloneCount := 0
	restoreCount := 0
	for _, pvcName := range pvcNames {
		if len(pvcName) > 6 && pvcName[len(pvcName)-6:] == "-clone" {
			cloneCount++
		} else {
			restoreCount++
		}
	}

	if cloneCount > 0 {
		if err := r.waitForPodsStatus(ctx, "load-test=true,load-test-role=clone-flog", "Running", cloneCount); err != nil {
			return err
		}
	}
	if restoreCount > 0 {
		if err := r.waitForPodsStatus(ctx, "load-test=true,load-test-role=restore-flog", "Running", restoreCount); err != nil {
			return err
		}
	}

	// Resize PVCs
	if err := r.pvcClient.ResizeList(ctx, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResizeStage2); err != nil {
		return err
	}
	if err := r.pvcClient.WaitForResize(ctx, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResizeStage2, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	return nil
}

// createVolumeSnapshot creates a VolumeSnapshot
func (r *StressTestRunner) createVolumeSnapshot(ctx context.Context, pvcIndex int, snapshotName string) error {
	pvcName := fmt.Sprintf("pvc-test-%d", pvcIndex)
	snapshot := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "snapshot.storage.k8s.io/v1",
			"kind":       "VolumeSnapshot",
			"metadata": map[string]interface{}{
				"name": snapshotName,
				"labels": map[string]interface{}{
					"load-test":      "true",
					"load-test-role": "snapshot",
				},
			},
			"spec": map[string]interface{}{
				"source": map[string]interface{}{
					"persistentVolumeClaimName": pvcName,
				},
			},
		},
	}

	_, err := r.snapshotClient.Create(ctx, r.config.Namespace, snapshot)
	return err
}

// createRestorePodAndPVC creates a pod and PVC restored from a snapshot
func (r *StressTestRunner) createRestorePodAndPVC(ctx context.Context, snapshotName, pvcName, podName string) error {
	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "restore",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(r.config.PVCSize),
				},
			},
			StorageClassName: &r.config.StorageClassName,
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
				Kind:     "VolumeSnapshot",
				Name:     snapshotName,
			},
		},
	}

	_, err := r.pvcClient.Create(ctx, r.config.Namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to create restore PVC %s: %w", pvcName, err)
	}
	logger.Debug("Created restore PVC %s/%s", r.config.Namespace, pvcName)

	// Create Pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "restore",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SchedulerName: r.config.SchedulerName,
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "data-checker",
					Image:   "alpine",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`set -e
dir="/usr/share/test-data"
echo "Listing directory contents:"
ls -lah "$dir" || true
checked=0
skipped=0
for sumf in "$dir"/*.sha256; do
  [ -e "$sumf" ] || continue
  case "$sumf" in *.tmp) skipped=$((skipped+1)); continue ;; esac
  if [ ! -s "$sumf" ]; then
    echo "SKIP empty checksum: $sumf"
    skipped=$((skipped+1))
    continue
  fi
  sha256sum -c "$sumf"
  checked=$((checked+1))
done
echo "Data check passed (checked: $checked, skipped: $skipped)"`,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-volume",
							MountPath: "/usr/share/test-data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-volume",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	_, err = r.podClient.Create(ctx, r.config.Namespace, pod)
	if err != nil {
		return fmt.Errorf("failed to create restore pod %s: %w", podName, err)
	}

	logger.Debug("Created restore pod %s/%s", r.config.Namespace, podName)
	return nil
}

// createClonePodAndPVC creates a pod and PVC cloned from an original PVC
func (r *StressTestRunner) createClonePodAndPVC(ctx context.Context, originalIndex int, pvcName, podName, cloneSize string) error {
	originalPVCName := fmt.Sprintf("pvc-test-%d", originalIndex)

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "clone",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(cloneSize),
				},
			},
			StorageClassName: &r.config.StorageClassName,
			DataSource: &corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: originalPVCName,
			},
		},
	}

	_, err := r.pvcClient.Create(ctx, r.config.Namespace, pvc)
	if err != nil {
		return fmt.Errorf("failed to create clone PVC %s: %w", pvcName, err)
	}
	logger.Debug("Created clone PVC %s/%s", r.config.Namespace, pvcName)

	// Create Pod (same as restore pod)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": "clone",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SchedulerName: r.config.SchedulerName,
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "data-checker",
					Image:   "alpine",
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`set -e
dir="/usr/share/test-data"
echo "Listing directory contents:"
ls -lah "$dir" || true
checked=0
skipped=0
for sumf in "$dir"/*.sha256; do
  [ -e "$sumf" ] || continue
  case "$sumf" in *.tmp) skipped=$((skipped+1)); continue ;; esac
  if [ ! -s "$sumf" ]; then
    echo "SKIP empty checksum: $sumf"
    skipped=$((skipped+1))
    continue
  fi
  sha256sum -c "$sumf"
  checked=$((checked+1))
done
echo "Data check passed (checked: $checked, skipped: $skipped)"`,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data-volume",
							MountPath: "/usr/share/test-data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data-volume",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	_, err = r.podClient.Create(ctx, r.config.Namespace, pod)
	if err != nil {
		return fmt.Errorf("failed to create clone pod %s: %w", podName, err)
	}

	logger.Debug("Created clone pod %s/%s", r.config.Namespace, podName)
	return nil
}

// createFlogPodForPVC creates a flog pod for an existing PVC
func (r *StressTestRunner) createFlogPodForPVC(ctx context.Context, podName, pvcName, role string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"load-test":      "true",
				"load-test-role": role,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SchedulerName: r.config.SchedulerName,
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "flog-generator",
					Image: "ex42zav/flog:0.4.3",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("20Mi"),
						},
					},
					Command: []string{"/bin/sh", "-c"},
					Args: []string{
						`echo "Starting flog generator..."
trap 'echo "Termination signal received, exiting..."; exit 0' TERM INT
while true; do
  /srv/flog/flog -b "${FLOG_BATCH_SIZE}" -f "${FLOG_LOG_FORMAT}" 2>&1 | tee -a /var/log/flog/fake.log
  if ! touch /var/log/flog/fake.log; then
    echo "Error: Unable to write to /var/log/flog/fake.log" >&2
    exit 1
  fi
  sleep ${FLOG_TIME_INTERVAL}
done`,
					},
					Env: []corev1.EnvVar{
						{Name: "FLOG_BATCH_SIZE", Value: "10700"},
						{Name: "FLOG_TIME_INTERVAL", Value: "1"},
						{Name: "FLOG_LOG_FORMAT", Value: "json"},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nginx-persistent-storage",
							MountPath: "/var/log/flog",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "nginx-persistent-storage",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	_, err := r.podClient.Create(ctx, r.config.Namespace, pod)
	if err != nil {
		return fmt.Errorf("failed to create flog pod %s: %w", podName, err)
	}

	logger.Debug("Created flog pod %s/%s", r.config.Namespace, podName)
	return nil
}

// cleanup cleans up all resources created during the test
func (r *StressTestRunner) cleanup(ctx context.Context) error {
	// Delete pods
	if err := r.podClient.DeleteByLabelSelector(ctx, r.config.Namespace, "load-test=true"); err != nil {
		return fmt.Errorf("failed to delete pods: %w", err)
	}

	// Delete PVCs
	if err := r.pvcClient.DeleteByLabelSelector(ctx, r.config.Namespace, "load-test=true"); err != nil {
		return fmt.Errorf("failed to delete PVCs: %w", err)
	}

	// Delete VolumeSnapshots
	if err := r.snapshotClient.DeleteByLabelSelector(ctx, r.config.Namespace, "load-test=true"); err != nil {
		return fmt.Errorf("failed to delete VolumeSnapshots: %w", err)
	}

	// Wait for deletion
	if err := r.pvcClient.WaitForDeletion(ctx, r.config.Namespace, "load-test=true", r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Delete namespace if requested
	if r.config.DeleteNamespace && r.config.Namespace != "default" {
		if err := r.namespaceClient.Delete(ctx, r.config.Namespace); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		logger.Debug("Deleted namespace %s", r.config.Namespace)
	}

	return nil
}
