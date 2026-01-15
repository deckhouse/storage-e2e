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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	pkgkubernetes "github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

var (
	// VolumeSnapshotGVR is the GroupVersionResource for VolumeSnapshot
	VolumeSnapshotGVR = schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshots",
	}
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
	Iterations       int // Number of test iterations to run (default: 1)
	SchedulerName    string
	Mode             TestMode

	// Resize configuration
	PVCSizeAfterResize       string
	PVCSizeAfterResizeStage2 string

	// Snapshot configuration
	SnapshotsPerPVC int

	// Test order for snapshot_resize_cloning mode
	TestOrder []TestStep

	// Timeouts and retries
	MaxAttempts int
	Interval    time.Duration

	// Cleanup - if true, removes all resources including namespace after test
	Cleanup bool
}

// DefaultConfig returns a config with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		SchedulerName:   "default-scheduler",
		Mode:            ModeFlog,
		SnapshotsPerPVC: 1,
		Iterations:      1, // Run test once by default
		MaxAttempts:     0, // 0 means infinite
		Interval:        5 * time.Second,
		Cleanup:         false,
		TestOrder:       []TestStep{StepRestoreFromSnapshot, StepResize, StepClone},
	}
}

// StressTestRunner runs stress tests
type StressTestRunner struct {
	config        *Config
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	restConfig    *rest.Config
}

// NewStressTestRunner creates a new stress test runner
func NewStressTestRunner(config *Config, restConfig *rest.Config) (*StressTestRunner, error) {
	// Create native Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Create dynamic client for custom resources like VolumeSnapshots
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &StressTestRunner{
		config:        config,
		clientset:     clientset,
		dynamicClient: dynamicClient,
		restConfig:    restConfig,
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
	if c.Iterations <= 0 {
		return fmt.Errorf("iterations must be > 0")
	}

	switch c.Mode {
	case ModeCheckCloning:
		// PVC for cloning will be auto-generated (pvc-test-1)
	case ModeCheckRestoringFromSnapshot:
		// Snapshot name will be auto-generated (snapshot-test-1-1)
	case ModeSnapshotResizeCloning:
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
	_, err := r.clientset.CoreV1().Namespaces().Get(ctx, r.config.Namespace, metav1.GetOptions{})
	if err != nil {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: r.config.Namespace,
			},
		}
		_, err = r.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
		logger.Debug("Created namespace %s", r.config.Namespace)
	}

	// Label namespace
	_, err = r.clientset.CoreV1().Namespaces().Patch(ctx, r.config.Namespace, types.JSONPatchType, []byte(`[{"op": "add", "path": "/metadata/labels/load-test", "value": "true"}]`), metav1.PatchOptions{})
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

// createPodAndPVC is a generic function to create a PVC and Pod
// Pass nil for pvc if you only want to create a pod (for existing PVC)
func (r *StressTestRunner) createPodAndPVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim, pod *corev1.Pod) error {
	// Create PVC if provided
	if pvc != nil {
		_, err := r.clientset.CoreV1().PersistentVolumeClaims(r.config.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create PVC %s: %w", pvc.Name, err)
		}
		logger.Debug("Created PVC %s/%s", r.config.Namespace, pvc.Name)
	}

	// Create Pod
	_, err := r.clientset.CoreV1().Pods(r.config.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create pod %s: %w", pod.Name, err)
	}
	logger.Debug("Created pod %s/%s", r.config.Namespace, pod.Name)

	return nil
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

	return r.createPodAndPVC(ctx, pvc, pod)
}

// createFlogPodAndPVC creates a flog pod and optionally a PVC
// If createPVC is false, pvcName must be an existing PVC name
func (r *StressTestRunner) createFlogPodAndPVC(ctx context.Context, podName, pvcName, role string, createPVC, withAntiAffinity bool, firstStart string) error {
	var pvc *corev1.PersistentVolumeClaim

	// Create PVC if requested
	if createPVC {
		pvc = &corev1.PersistentVolumeClaim{
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
	}

	// Build script
	script := `echo "Starting flog generator..."`
	if firstStart != "" {
		script += `
ls -A /var/log/flog
folder_files=$(ls -A /var/log/flog 2>/dev/null | grep -v '^lost+found$')
echo "folder_files: $folder_files"
echo "FIRST_START: $FIRST_START"

if [ -n "$folder_files" ] && [ "$FIRST_START" = "true" ]; then
  echo "Error: leftover files found in /var/log/flog" >&2
  exit 1
fi`
	}
	script += `
trap 'echo "Termination signal received, exiting..."; exit 0' TERM INT
while true; do
  /srv/flog/flog -b "${FLOG_BATCH_SIZE}" -f "${FLOG_LOG_FORMAT}" 2>&1 | tee -a /var/log/flog/fake.log
  if ! touch /var/log/flog/fake.log; then
    echo "Error: Unable to write to /var/log/flog/fake.log" >&2
    exit 1
  fi
  sleep ${FLOG_TIME_INTERVAL}
done`

	env := []corev1.EnvVar{
		{Name: "FLOG_BATCH_SIZE", Value: "10700"},
		{Name: "FLOG_TIME_INTERVAL", Value: "1"},
		{Name: "FLOG_LOG_FORMAT", Value: "json"},
	}
	if firstStart != "" {
		env = append(env, corev1.EnvVar{Name: "FIRST_START", Value: firstStart})
	}

	// Build pod labels
	labels := map[string]string{
		"load-test": "true",
	}
	if role != "" {
		labels["load-test-role"] = role
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   podName,
			Labels: labels,
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
					Args:    []string{script},
					Env:     env,
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

	// Add anti-affinity if requested
	if withAntiAffinity {
		pod.Spec.Affinity = &corev1.Affinity{
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
		}
	}

	return r.createPodAndPVC(ctx, pvc, pod)
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

	_, err := r.dynamicClient.Resource(VolumeSnapshotGVR).Namespace(r.config.Namespace).Create(ctx, snapshot, metav1.CreateOptions{})
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

	return r.createPodAndPVC(ctx, pvc, pod)
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

	return r.createPodAndPVC(ctx, pvc, pod)
}

// createCheckFSPodAndPVC creates a pod and PVC for filesystem check only
func (r *StressTestRunner) createCheckFSPodAndPVC(ctx context.Context, index int) error {
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
			VolumeMode:       func() *corev1.PersistentVolumeMode { mode := corev1.PersistentVolumeFilesystem; return &mode }(),
		},
	}

	// Create Pod - just checks if filesystem is mounted correctly
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
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "nginx",
					Image:   "nginx",
					Command: []string{"/bin/bash"},
					Args: []string{
						"-c",
						"df -T | grep '/usr/share/test-data' | grep 'ext4'",
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nginx-persistent-storage",
							MountPath: "/usr/share/test-data",
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

	return r.createPodAndPVC(ctx, pvc, pod)
}

// createCloneCheckPodAndPVC creates a pod and PVC cloned from a source PVC
func (r *StressTestRunner) createCloneCheckPodAndPVC(ctx context.Context, index int, sourcePVCName string) error {
	pvcName := fmt.Sprintf("pvc-test-%d", index)
	podName := fmt.Sprintf("pod-test-%d", index)

	// Create cloned PVC
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
			DataSource: &corev1.TypedLocalObjectReference{
				Kind: "PersistentVolumeClaim",
				Name: sourcePVCName,
			},
		},
	}

	// Create Pod - checks filesystem
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
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "nginx",
					Image:   "nginx",
					Command: []string{"/bin/bash"},
					Args: []string{
						"-c",
						"df -T | grep '/usr/share/test-data' | grep 'ext4'",
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nginx-persistent-storage",
							MountPath: "/usr/share/test-data",
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

	return r.createPodAndPVC(ctx, pvc, pod)
}

// createRestoreCheckPodAndPVC creates a pod and PVC restored from a snapshot
func (r *StressTestRunner) createRestoreCheckPodAndPVC(ctx context.Context, index int, snapshotName string) error {
	pvcName := fmt.Sprintf("pvc-test-%d", index)
	podName := fmt.Sprintf("pod-test-%d", index)

	// Create PVC restored from snapshot
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
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: func() *string { s := "snapshot.storage.k8s.io"; return &s }(),
				Kind:     "VolumeSnapshot",
				Name:     snapshotName,
			},
		},
	}

	// Create Pod - checks filesystem and verifies data integrity
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
			Tolerations: []corev1.Toleration{
				{
					Key: "node-role.kubernetes.io/control-plane",
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "nginx",
					Image:   "nginx",
					Command: []string{"/bin/bash"},
					Args: []string{
						"-c",
						"df -T | grep '/usr/share/test-data' | grep 'ext4'; cd /usr/share/test-data/; ls -lah; sha256sum -c ./*.sha256; echo test>/usr/share/test-data/testfile-for-write",
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "nginx-persistent-storage",
							MountPath: "/usr/share/test-data",
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

	return r.createPodAndPVC(ctx, pvc, pod)
}

// runFlogMode runs the flog mode test
func (r *StressTestRunner) runFlogMode(ctx context.Context) error {
	// Create all pods and PVCs in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, r.config.PodsCount)

	logger.Debug("Creating %d pods and PVCs in parallel", r.config.PodsCount)
	for i := 1; i <= r.config.PodsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			pvcName := fmt.Sprintf("pvc-test-%d", index)
			podName := fmt.Sprintf("pod-test-%d", index)
			firstStart := ""
			if index == 1 {
				firstStart = "true"
			}
			// createPVC=true, withAntiAffinity=true, firstStart check
			if err := r.createFlogPodAndPVC(ctx, podName, pvcName, "", true, true, firstStart); err != nil {
				errChan <- fmt.Errorf("failed to create pod/PVC %d: %w", index, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs to be bound
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to be running
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true", "Running", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Resize if configured
	if r.config.PVCSizeAfterResize != "" {
		pvcNames := make([]string, r.config.PodsCount)
		for i := 1; i <= r.config.PodsCount; i++ {
			pvcNames[i-1] = fmt.Sprintf("pvc-test-%d", i)
		}
		if err := pkgkubernetes.ResizeList(ctx, r.clientset, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResize); err != nil {
			return err
		}
		if err := pkgkubernetes.WaitForPVCsResized(ctx, r.clientset, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResize, r.config.MaxAttempts, r.config.Interval); err != nil {
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
// Creates PVCs and pods that simply check if filesystem is mounted correctly
func (r *StressTestRunner) runCheckFSOnlyMode(ctx context.Context) error {
	// Create all pods and PVCs in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, r.config.PodsCount)

	logger.Debug("Creating %d pods and PVCs for filesystem check in parallel", r.config.PodsCount)
	for i := 1; i <= r.config.PodsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := r.createCheckFSPodAndPVC(ctx, index); err != nil {
				errChan <- fmt.Errorf("failed to create filesystem check pod/PVC %d: %w", index, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs to be bound
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to complete (filesystem check is quick)
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true", "Completed", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Cleanup if requested
	if r.config.Cleanup {
		return r.cleanup(ctx)
	}

	return nil
}

// runCheckCloningMode runs the check_cloning mode test
// Creates PVCs cloned from pvc-test-1 and verifies they work
func (r *StressTestRunner) runCheckCloningMode(ctx context.Context) error {
	sourcePVCName := "pvc-test-1" // Auto-generated source PVC name

	// Create all cloned PVCs and pods in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, r.config.PodsCount)

	logger.Debug("Creating %d cloned PVCs from %s in parallel", r.config.PodsCount, sourcePVCName)
	for i := 1; i <= r.config.PodsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := r.createCloneCheckPodAndPVC(ctx, index, sourcePVCName); err != nil {
				errChan <- fmt.Errorf("failed to create clone check pod/PVC %d: %w", index, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs to be bound
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to complete
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true", "Completed", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Cleanup if requested
	if r.config.Cleanup {
		return r.cleanup(ctx)
	}

	return nil
}

// runCheckRestoringFromSnapshotMode runs the check_restoring_from_snapshot mode test
// Creates PVCs restored from a snapshot and verifies data integrity
func (r *StressTestRunner) runCheckRestoringFromSnapshotMode(ctx context.Context) error {
	snapshotName := "snapshot-test-1-1" // Auto-generated snapshot name

	// Create all restored PVCs and pods in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, r.config.PodsCount)

	logger.Debug("Creating %d restored PVCs from snapshot %s in parallel", r.config.PodsCount, snapshotName)
	for i := 1; i <= r.config.PodsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := r.createRestoreCheckPodAndPVC(ctx, index, snapshotName); err != nil {
				errChan <- fmt.Errorf("failed to create restore check pod/PVC %d: %w", index, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs to be bound
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to complete (with data integrity check)
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true", "Completed", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Cleanup if requested
	if r.config.Cleanup {
		return r.cleanup(ctx)
	}

	return nil
}

// runSnapshotResizeCloningMode runs the snapshot_resize_cloning mode test
func (r *StressTestRunner) runSnapshotResizeCloningMode(ctx context.Context) error {
	// Create all original pods and PVCs in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, r.config.PodsCount)

	logger.Debug("Creating %d original pods and PVCs in parallel", r.config.PodsCount)
	for i := 1; i <= r.config.PodsCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := r.createOriginalPodAndPVC(ctx, index); err != nil {
				errChan <- fmt.Errorf("failed to create original pod/PVC %d: %w", index, err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs to be bound
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=original", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	// Wait for pods to be running
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=original", "Running", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
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
			if err := pkgkubernetes.ResizeList(ctx, r.clientset, r.config.Namespace, originalPVCNames, r.config.PVCSizeAfterResize); err != nil {
				return err
			}
			if err := pkgkubernetes.WaitForPVCsResized(ctx, r.clientset, r.config.Namespace, originalPVCNames, r.config.PVCSizeAfterResize, r.config.MaxAttempts, r.config.Interval); err != nil {
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
	// Create all snapshots in parallel
	totalSnapshots := r.config.PodsCount * r.config.SnapshotsPerPVC
	var wg sync.WaitGroup
	errChan := make(chan error, totalSnapshots)

	logger.Debug("Creating %d snapshots in parallel", totalSnapshots)
	for k := 1; k <= r.config.PodsCount; k++ {
		for s := 1; s <= r.config.SnapshotsPerPVC; s++ {
			wg.Add(1)
			go func(pvcIndex, snapshotNum int) {
				defer wg.Done()
				snapshotName := fmt.Sprintf("snapshot-test-%d-%d", pvcIndex, snapshotNum)
				if err := r.createVolumeSnapshot(ctx, pvcIndex, snapshotName); err != nil {
					errChan <- fmt.Errorf("failed to create snapshot %s: %w", snapshotName, err)
				}
			}(k, s)
		}
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Wait for all snapshots to be ready
	attempt := 0
	for {
		snapshots, err := r.dynamicClient.Resource(VolumeSnapshotGVR).Namespace(r.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "load-test-role=snapshot",
		})
		if err != nil {
			return err
		}

		readyCount := 0
		for _, snapshot := range snapshots.Items {
			status, found, err := unstructured.NestedMap(snapshot.Object, "status")
			if found && err == nil {
				if readyToUse, found, err := unstructured.NestedBool(status, "readyToUse"); found && err == nil && readyToUse {
					readyCount++
				}
			}
		}

		if readyCount >= totalSnapshots {
			break
		}

		if readyCount > 0 {
			attempt++
		}

		if r.config.MaxAttempts > 0 && attempt >= r.config.MaxAttempts {
			return fmt.Errorf("timeout waiting for VolumeSnapshots to be ready: %d/%d ready after %d attempts", readyCount, totalSnapshots, r.config.MaxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.config.Interval):
		}
	}

	// Create all restore PVCs and pods in parallel
	var wg2 sync.WaitGroup
	var mu sync.Mutex
	errChan2 := make(chan error, r.config.PodsCount*r.config.SnapshotsPerPVC)

	for k := 1; k <= r.config.PodsCount; k++ {
		for s := 1; s <= r.config.SnapshotsPerPVC; s++ {
			wg2.Add(1)
			go func(pvcIndex, snapshotNum int) {
				defer wg2.Done()
				snapshotName := fmt.Sprintf("snapshot-test-%d-%d", pvcIndex, snapshotNum)
				pvcName := fmt.Sprintf("pvc-test-%d-restore-%d", pvcIndex, snapshotNum)
				podName := fmt.Sprintf("pod-test-%d-restore-%d", pvcIndex, snapshotNum)
				if err := r.createRestorePodAndPVC(ctx, snapshotName, pvcName, podName); err != nil {
					errChan2 <- fmt.Errorf("failed to create restore pod/PVC %s: %w", podName, err)
					return
				}
				mu.Lock()
				*restorePVCNames = append(*restorePVCNames, pvcName)
				mu.Unlock()
			}(k, s)
		}
	}

	wg2.Wait()
	close(errChan2)

	// Check for errors
	for err := range errChan2 {
		if err != nil {
			return err
		}
	}

	totalRestore := r.config.PodsCount * r.config.SnapshotsPerPVC
	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=restore", totalRestore, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=restore", "Completed", totalRestore, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	return nil
}

// executeCloneStep executes the clone step
func (r *StressTestRunner) executeCloneStep(ctx context.Context, clonePVCNames *[]string) error {
	// Create clone PVCs and pods in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, r.config.PodsCount)

	for k := 1; k <= r.config.PodsCount; k++ {
		wg.Add(1)
		go func(pvcIndex int) {
			defer wg.Done()

			// Get current size of original PVC
			pvc, err := r.clientset.CoreV1().PersistentVolumeClaims(r.config.Namespace).Get(ctx, fmt.Sprintf("pvc-test-%d", pvcIndex), metav1.GetOptions{})
			if err != nil {
				errChan <- fmt.Errorf("failed to get PVC pvc-test-%d: %w", pvcIndex, err)
				return
			}
			currentSize := r.config.PVCSize
			if pvc.Status.Capacity != nil {
				if size, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
					currentSize = size.String()
				}
			}

			pvcName := fmt.Sprintf("pvc-test-%d-clone", pvcIndex)
			podName := fmt.Sprintf("pod-test-%d-clone", pvcIndex)
			if err := r.createClonePodAndPVC(ctx, pvcIndex, pvcName, podName, currentSize); err != nil {
				errChan <- fmt.Errorf("failed to create clone pod/PVC %s: %w", podName, err)
				return
			}

			mu.Lock()
			*clonePVCNames = append(*clonePVCNames, pvcName)
			mu.Unlock()
		}(k)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	if err := pkgkubernetes.WaitForPVCsBound(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=clone", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}
	if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=clone", "Completed", r.config.PodsCount, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	return nil
}

// executeStage2 executes stage 2: flog pods and resize
func (r *StressTestRunner) executeStage2(ctx context.Context, pvcNames []string) error {
	// Create flog pods for each PVC in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, len(pvcNames))

	for _, pvcName := range pvcNames {
		wg.Add(1)
		go func(pvcName string) {
			defer wg.Done()
			podName := fmt.Sprintf("%s-flog", pvcName)
			role := "clone-flog"
			if len(pvcName) > 8 && pvcName[len(pvcName)-8:] == "-restore" {
				role = "restore-flog"
			}
			// createPVC=false (PVC already exists), withAntiAffinity=false, no firstStart check
			if err := r.createFlogPodAndPVC(ctx, podName, pvcName, role, false, false, ""); err != nil {
				errChan <- fmt.Errorf("failed to create flog pod %s: %w", podName, err)
			}
		}(pvcName)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
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
		if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=clone-flog", "Running", cloneCount, r.config.MaxAttempts, r.config.Interval); err != nil {
			return err
		}
	}
	if restoreCount > 0 {
		if err := pkgkubernetes.WaitForPodsStatus(ctx, r.clientset, r.config.Namespace, "load-test=true,load-test-role=restore-flog", "Running", restoreCount, r.config.MaxAttempts, r.config.Interval); err != nil {
			return err
		}
	}

	// Resize PVCs
	if err := pkgkubernetes.ResizeList(ctx, r.clientset, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResizeStage2); err != nil {
		return err
	}
	if err := pkgkubernetes.WaitForPVCsResized(ctx, r.clientset, r.config.Namespace, pvcNames, r.config.PVCSizeAfterResizeStage2, r.config.MaxAttempts, r.config.Interval); err != nil {
		return err
	}

	return nil
}

// cleanup cleans up all resources created during the test
func (r *StressTestRunner) cleanup(ctx context.Context) error {
	// Delete pods
	if err := r.clientset.CoreV1().Pods(r.config.Namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: "load-test=true",
	}); err != nil {
		return fmt.Errorf("failed to delete pods: %w", err)
	}

	// Delete PVCs
	if err := r.clientset.CoreV1().PersistentVolumeClaims(r.config.Namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: "load-test=true",
	}); err != nil {
		return fmt.Errorf("failed to delete PVCs: %w", err)
	}

	// Delete VolumeSnapshots
	snapshots, err := r.dynamicClient.Resource(VolumeSnapshotGVR).Namespace(r.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "load-test=true",
	})
	if err != nil {
		return fmt.Errorf("failed to list VolumeSnapshots: %w", err)
	}
	var snapshotWg sync.WaitGroup
	snapshotErrChan := make(chan error, len(snapshots.Items))
	for _, snapshot := range snapshots.Items {
		snapshotWg.Add(1)
		go func(name string) {
			defer snapshotWg.Done()
			if err := r.dynamicClient.Resource(VolumeSnapshotGVR).Namespace(r.config.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
				snapshotErrChan <- fmt.Errorf("failed to delete VolumeSnapshot %s: %w", name, err)
			}
		}(snapshot.GetName())
	}
	snapshotWg.Wait()
	close(snapshotErrChan)
	for err := range snapshotErrChan {
		if err != nil {
			return err
		}
	}

	// Wait for PVCs deletion
	attempt := 0
	for {
		pvcs, err := r.clientset.CoreV1().PersistentVolumeClaims(r.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "load-test=true",
		})
		if err != nil {
			// If listing fails, assume PVCs are deleted
			break
		}
		if len(pvcs.Items) == 0 {
			break
		}
		attempt++
		if r.config.MaxAttempts > 0 && attempt >= r.config.MaxAttempts {
			return fmt.Errorf("timeout waiting for PVCs to be deleted: %d remaining after %d attempts", len(pvcs.Items), r.config.MaxAttempts)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.config.Interval):
		}
	}

	// Delete namespace if cleanup is enabled and not default namespace
	if r.config.Namespace != "default" {
		if err := r.clientset.CoreV1().Namespaces().Delete(ctx, r.config.Namespace, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		logger.Debug("Deleted namespace %s", r.config.Namespace)
	}

	return nil
}
