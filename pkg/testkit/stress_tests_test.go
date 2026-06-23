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
	"strings"
	"testing"

	"github.com/deckhouse/storage-e2e/internal/config"
)

// baseConfig returns a minimal valid Config for ModeFlog.
func baseConfig() *Config {
	return &Config{
		Namespace:        "ns",
		StorageClassName: "sc",
		PVCSize:          "1Gi",
		PodsCount:        1,
		Iterations:       1,
		Mode:             ModeFlog,
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(c *Config)
		wantErr   string // substring; empty == no error
		afterHook func(t *testing.T, c *Config)
	}{
		{
			name:    "valid flog config",
			mutate:  func(*Config) {},
			wantErr: "",
		},
		{
			name:    "missing namespace",
			mutate:  func(c *Config) { c.Namespace = "" },
			wantErr: "namespace",
		},
		{
			name:    "missing storage class",
			mutate:  func(c *Config) { c.StorageClassName = "" },
			wantErr: "storage class",
		},
		{
			name:    "missing PVC size",
			mutate:  func(c *Config) { c.PVCSize = "" },
			wantErr: "PVC size",
		},
		{
			name:    "zero pods count",
			mutate:  func(c *Config) { c.PodsCount = 0 },
			wantErr: "pods count",
		},
		{
			name:    "negative iterations",
			mutate:  func(c *Config) { c.Iterations = 0 },
			wantErr: "iterations",
		},
		{
			name: "snapshot-only with default SnapshotsPerPVC gets defaulted to 1",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotOnly
				c.SnapshotsPerPVC = 0
			},
			wantErr: "",
			afterHook: func(t *testing.T, c *Config) {
				if c.SnapshotsPerPVC != 1 {
					t.Errorf("SnapshotsPerPVC=%d, want 1 (default)", c.SnapshotsPerPVC)
				}
			},
		},
		{
			name: "snapshot_resize_cloning requires SnapshotsPerPVC > 0",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotResizeCloning
				c.SnapshotsPerPVC = 0
				c.PVCSizeAfterResize = "2Gi"
				c.PVCSizeAfterResizeStage2 = "3Gi"
				c.TestOrder = []TestStep{StepResize}
			},
			wantErr: "snapshots per PVC",
		},
		{
			name: "snapshot_resize_cloning rejects invalid step",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotResizeCloning
				c.SnapshotsPerPVC = 1
				c.PVCSizeAfterResize = "2Gi"
				c.PVCSizeAfterResizeStage2 = "3Gi"
				c.TestOrder = []TestStep{"bogus"}
			},
			wantErr: "invalid test step",
		},
		{
			name: "snapshot_resize_cloning resize step requires PVCSizeAfterResize",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotResizeCloning
				c.SnapshotsPerPVC = 1
				c.PVCSizeAfterResize = ""
				c.PVCSizeAfterResizeStage2 = "3Gi"
				c.TestOrder = []TestStep{StepResize}
			},
			wantErr: "PVC size after resize",
		},
		{
			name: "snapshot_resize_cloning clone/restore step requires Stage2 size",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotResizeCloning
				c.SnapshotsPerPVC = 1
				c.PVCSizeAfterResize = "2Gi"
				c.PVCSizeAfterResizeStage2 = ""
				c.TestOrder = []TestStep{StepClone}
			},
			wantErr: "stage2",
		},
		{
			name: "snapshot_resize_cloning happy path",
			mutate: func(c *Config) {
				c.Mode = ModeSnapshotResizeCloning
				c.SnapshotsPerPVC = 2
				c.PVCSizeAfterResize = "2Gi"
				c.PVCSizeAfterResizeStage2 = "3Gi"
				c.TestOrder = []TestStep{StepRestoreFromSnapshot, StepResize, StepClone}
			},
			wantErr: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseConfig()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.afterHook != nil {
					tc.afterHook(t, c)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDefaultConfig_UsesDefaultsWhenEnvUnset(t *testing.T) {
	// Snapshot package-level env-derived globals and restore them so test
	// ordering (`-shuffle=on`) cannot leak between cases. `t.Setenv` only
	// affects new os.Getenv reads — the config package already cached env
	// values into package vars at init.
	defer withConfigSnapshot(t)()

	config.StressTestPVCSize = ""
	config.StressTestPodsCount = ""
	config.StressTestPVCSizeAfterResize = ""
	config.StressTestPVCSizeAfterResizeStage2 = ""
	config.StressTestSnapshotsPerPVC = ""
	config.StressTestMaxAttempts = ""
	config.StressTestInterval = ""
	config.StressTestCleanup = ""

	got := DefaultConfig()

	if got.PVCSize != config.StressTestPVCSizeDefaultValue {
		t.Errorf("PVCSize=%q, want default %q", got.PVCSize, config.StressTestPVCSizeDefaultValue)
	}
	if got.PVCSizeAfterResize != config.StressTestPVCSizeAfterResizeDefaultValue {
		t.Errorf("PVCSizeAfterResize=%q, want default", got.PVCSizeAfterResize)
	}
	if got.PVCSizeAfterResizeStage2 != config.StressTestPVCSizeAfterResizeStage2DefaultValue {
		t.Errorf("PVCSizeAfterResizeStage2=%q, want default", got.PVCSizeAfterResizeStage2)
	}
	if got.PodsCount <= 0 {
		t.Errorf("PodsCount=%d, want > 0", got.PodsCount)
	}
	if got.SnapshotsPerPVC <= 0 {
		t.Errorf("SnapshotsPerPVC=%d, want > 0", got.SnapshotsPerPVC)
	}
	if got.MaxAttempts <= 0 {
		t.Errorf("MaxAttempts=%d, want > 0", got.MaxAttempts)
	}
	if got.Interval <= 0 {
		t.Errorf("Interval=%v, want > 0", got.Interval)
	}
	if !got.Cleanup {
		t.Errorf("Cleanup=false; default should be true")
	}
	if got.Iterations != 1 {
		t.Errorf("Iterations=%d, want 1", got.Iterations)
	}
	if got.Mode != ModeFlog {
		t.Errorf("Mode=%q, want %q", got.Mode, ModeFlog)
	}
	if got.SchedulerName != "default-scheduler" {
		t.Errorf("SchedulerName=%q, want %q", got.SchedulerName, "default-scheduler")
	}
	if len(got.TestOrder) != 3 {
		t.Errorf("TestOrder len=%d, want 3", len(got.TestOrder))
	}
}

func TestDefaultConfig_RespectsEnvOverrides(t *testing.T) {
	defer withConfigSnapshot(t)()

	config.StressTestPVCSize = "9Gi"
	config.StressTestPodsCount = "7"
	config.StressTestPVCSizeAfterResize = "10Gi"
	config.StressTestPVCSizeAfterResizeStage2 = "11Gi"
	config.StressTestSnapshotsPerPVC = "5"
	config.StressTestMaxAttempts = "12"
	config.StressTestInterval = "3"
	config.StressTestCleanup = "false"

	got := DefaultConfig()

	if got.PVCSize != "9Gi" || got.PodsCount != 7 || got.PVCSizeAfterResize != "10Gi" ||
		got.PVCSizeAfterResizeStage2 != "11Gi" || got.SnapshotsPerPVC != 5 ||
		got.MaxAttempts != 12 || got.Interval.Seconds() != 3 || got.Cleanup {
		t.Errorf("env overrides not honored: %+v", got)
	}
}

// withConfigSnapshot captures the package-level config vars touched by
// DefaultConfig and returns a restore function. Use as
//
//	defer withConfigSnapshot(t)()
func withConfigSnapshot(t *testing.T) func() {
	t.Helper()
	snap := struct {
		PVCSize, PodsCount                              string
		PVCSizeAfterResize, PVCSizeAfterResizeStage2    string
		SnapshotsPerPVC, MaxAttempts, Interval, Cleanup string
	}{
		PVCSize:                  config.StressTestPVCSize,
		PodsCount:                config.StressTestPodsCount,
		PVCSizeAfterResize:       config.StressTestPVCSizeAfterResize,
		PVCSizeAfterResizeStage2: config.StressTestPVCSizeAfterResizeStage2,
		SnapshotsPerPVC:          config.StressTestSnapshotsPerPVC,
		MaxAttempts:              config.StressTestMaxAttempts,
		Interval:                 config.StressTestInterval,
		Cleanup:                  config.StressTestCleanup,
	}
	return func() {
		config.StressTestPVCSize = snap.PVCSize
		config.StressTestPodsCount = snap.PodsCount
		config.StressTestPVCSizeAfterResize = snap.PVCSizeAfterResize
		config.StressTestPVCSizeAfterResizeStage2 = snap.PVCSizeAfterResizeStage2
		config.StressTestSnapshotsPerPVC = snap.SnapshotsPerPVC
		config.StressTestMaxAttempts = snap.MaxAttempts
		config.StressTestInterval = snap.Interval
		config.StressTestCleanup = snap.Cleanup
	}
}
