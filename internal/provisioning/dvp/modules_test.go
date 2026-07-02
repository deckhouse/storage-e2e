/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dvp

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func mc(name string, enabled bool, deps ...string) *config.ModuleConfig {
	return &config.ModuleConfig{Name: name, Enabled: enabled, Dependencies: deps}
}

// levelNames flattens the ordered levels into name slices for comparison.
func levelNames(levels [][]*config.ModuleConfig) [][]string {
	out := make([][]string, len(levels))
	for i, level := range levels {
		names := make([]string, len(level))
		for j, m := range level {
			names[j] = m.Name
		}
		out[i] = names
	}
	return out
}

func TestBuildModuleLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		modules []*config.ModuleConfig
		want    [][]string
		wantErr bool
	}{
		{
			name:    "empty",
			modules: nil,
			want:    nil,
		},
		{
			name:    "no dependencies sorted by name",
			modules: []*config.ModuleConfig{mc("c", true), mc("a", true), mc("b", true)},
			want:    [][]string{{"a", "b", "c"}},
		},
		{
			name:    "linear chain",
			modules: []*config.ModuleConfig{mc("c", true, "b"), mc("b", true, "a"), mc("a", true)},
			want:    [][]string{{"a"}, {"b"}, {"c"}},
		},
		{
			name: "diamond",
			modules: []*config.ModuleConfig{
				mc("d", true, "b", "c"),
				mc("b", true, "a"),
				mc("c", true, "a"),
				mc("a", true),
			},
			want: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name:    "duplicate dependency edges are deduped",
			modules: []*config.ModuleConfig{mc("b", true, "a", "a"), mc("a", true)},
			want:    [][]string{{"a"}, {"b"}},
		},
		{
			name:    "unknown dependency",
			modules: []*config.ModuleConfig{mc("a", true, "ghost")},
			wantErr: true,
		},
		{
			name:    "self dependency",
			modules: []*config.ModuleConfig{mc("a", true, "a")},
			wantErr: true,
		},
		{
			name:    "cycle",
			modules: []*config.ModuleConfig{mc("a", true, "b"), mc("b", true, "a")},
			wantErr: true,
		},
		{
			name:    "duplicate module",
			modules: []*config.ModuleConfig{mc("a", true), mc("a", true)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			levels, err := buildModuleLevels(tt.modules)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildModuleLevels() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildModuleLevels() error = %v", err)
			}
			got := levelNames(levels)
			if len(got) != len(tt.want) {
				t.Fatalf("levels = %v, want %v", got, tt.want)
			}
			for i := range got {
				if !slices.Equal(got[i], tt.want[i]) {
					t.Errorf("level %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// recordingApplier records apply/waitReady calls in order and can be configured
// to fail readiness for specific modules (to exercise timeouts).
type recordingApplier struct {
	mu         sync.Mutex
	applied    []string
	waited     []string
	neverReady map[string]bool
	applyErr   map[string]error
}

func (a *recordingApplier) apply(ctx context.Context, m *config.ModuleConfig, registryRepo string) error {
	a.mu.Lock()
	a.applied = append(a.applied, m.Name)
	err := a.applyErr[m.Name]
	a.mu.Unlock()
	return err
}

func (a *recordingApplier) waitReady(ctx context.Context, moduleName string, timeout time.Duration) error {
	a.mu.Lock()
	a.waited = append(a.waited, moduleName)
	never := a.neverReady[moduleName]
	a.mu.Unlock()

	if !never {
		return nil
	}
	// Simulate a module that never converges: block until the caller's timeout.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	<-waitCtx.Done()
	return fmt.Errorf("module %q never became ready: %w", moduleName, waitCtx.Err())
}

// appliedBefore reports whether module a was applied before module b.
func appliedBefore(order []string, a, b string) bool {
	return slices.Index(order, a) < slices.Index(order, b)
}

func TestEnableModulesInLevelsEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	applier := &recordingApplier{}
	if err := enableModulesInLevels(context.Background(), applier, nil, "dev-registry", time.Second); err != nil {
		t.Fatalf("enableModulesInLevels() error = %v", err)
	}
	if len(applier.applied) != 0 || len(applier.waited) != 0 {
		t.Errorf("expected no calls, applied=%v waited=%v", applier.applied, applier.waited)
	}
}

func TestEnableModulesInLevelsDependencyOrder(t *testing.T) {
	t.Parallel()
	applier := &recordingApplier{}
	modules := []*config.ModuleConfig{
		mc("d", true, "b", "c"),
		mc("b", true, "a"),
		mc("c", true, "a"),
		mc("a", true),
	}
	if err := enableModulesInLevels(context.Background(), applier, modules, "dev-registry", time.Second); err != nil {
		t.Fatalf("enableModulesInLevels() error = %v", err)
	}

	// Every module must be applied and waited on.
	if len(applier.applied) != 4 || len(applier.waited) != 4 {
		t.Fatalf("applied=%v waited=%v, want 4 each", applier.applied, applier.waited)
	}
	// Dependencies applied before dependents.
	for _, pair := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
		if !appliedBefore(applier.applied, pair[0], pair[1]) {
			t.Errorf("module %q was not applied before %q (order %v)", pair[0], pair[1], applier.applied)
		}
	}
}

func TestEnableModulesInLevelsDisabledNotWaited(t *testing.T) {
	t.Parallel()
	applier := &recordingApplier{}
	modules := []*config.ModuleConfig{mc("a", true), mc("b", false)}
	if err := enableModulesInLevels(context.Background(), applier, modules, "", time.Second); err != nil {
		t.Fatalf("enableModulesInLevels() error = %v", err)
	}
	if !slices.Contains(applier.applied, "b") {
		t.Error("disabled module should still be applied")
	}
	if slices.Contains(applier.waited, "b") {
		t.Error("disabled module should not be waited on for readiness")
	}
}

func TestEnableModulesInLevelsModuleNeverReadyTimesOut(t *testing.T) {
	t.Parallel()
	applier := &recordingApplier{neverReady: map[string]bool{"b": true}}
	modules := []*config.ModuleConfig{mc("a", true), mc("b", true, "a")}
	err := enableModulesInLevels(context.Background(), applier, modules, "", 30*time.Millisecond)
	if err == nil {
		t.Fatal("enableModulesInLevels() error = nil, want timeout for module b")
	}
}

func TestEnableModulesInLevelsApplyErrorStopsBeforeDependents(t *testing.T) {
	t.Parallel()
	applier := &recordingApplier{applyErr: map[string]error{"a": fmt.Errorf("apply boom")}}
	modules := []*config.ModuleConfig{mc("a", true), mc("b", true, "a")}
	err := enableModulesInLevels(context.Background(), applier, modules, "", time.Second)
	if err == nil {
		t.Fatal("enableModulesInLevels() error = nil, want apply error")
	}
	if slices.Contains(applier.applied, "b") {
		t.Error("dependent module b applied despite dependency a failing")
	}
}
