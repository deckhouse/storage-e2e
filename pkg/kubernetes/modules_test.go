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

package kubernetes

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func TestConvertModuleSpecsToConfigs(t *testing.T) {
	t.Run("nil Settings becomes empty map", func(t *testing.T) {
		got := convertModuleSpecsToConfigs([]ModuleSpec{
			{Name: "foo", Version: 1, Enabled: true, Settings: nil},
		})
		if len(got) != 1 {
			t.Fatalf("len=%d, want 1", len(got))
		}
		if got[0].Settings == nil {
			t.Fatal("Settings is nil; expected non-nil empty map")
		}
		if len(got[0].Settings) != 0 {
			t.Errorf("Settings should be empty, got %v", got[0].Settings)
		}
	})

	t.Run("copies fields verbatim", func(t *testing.T) {
		specs := []ModuleSpec{
			{
				Name:               "csi-ceph",
				Version:            2,
				Enabled:            true,
				Settings:           map[string]interface{}{"foo": "bar"},
				Dependencies:       []string{"snapshot-controller"},
				ModulePullOverride: "pr131",
			},
			{
				Name:    "noop",
				Enabled: false,
			},
		}
		got := convertModuleSpecsToConfigs(specs)
		if len(got) != 2 {
			t.Fatalf("len=%d, want 2", len(got))
		}
		if got[0].Name != "csi-ceph" || got[0].Version != 2 || !got[0].Enabled {
			t.Errorf("got[0]=%+v", got[0])
		}
		if got[0].Settings["foo"] != "bar" {
			t.Errorf("settings not copied: %v", got[0].Settings)
		}
		if !equalStrings(got[0].Dependencies, []string{"snapshot-controller"}) {
			t.Errorf("Dependencies = %v", got[0].Dependencies)
		}
		if got[0].ModulePullOverride != "pr131" {
			t.Errorf("ModulePullOverride = %q", got[0].ModulePullOverride)
		}
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		got := convertModuleSpecsToConfigs(nil)
		if got == nil {
			t.Fatal("expected non-nil empty slice")
		}
		if len(got) != 0 {
			t.Errorf("len=%d, want 0", len(got))
		}
	})
}

func TestBuildModuleGraph(t *testing.T) {
	t.Run("empty input -> empty graph", func(t *testing.T) {
		g, err := buildModuleGraph(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(g.modules) != 0 || len(g.dependencies) != 0 || len(g.reverseDeps) != 0 {
			t.Errorf("non-empty graph: %+v", g)
		}
	})

	t.Run("builds dependency and reverse-dependency edges", func(t *testing.T) {
		modules := []*config.ModuleConfig{
			{Name: "a"},
			{Name: "b", Dependencies: []string{"a"}},
			{Name: "c", Dependencies: []string{"a", "b"}},
		}
		g, err := buildModuleGraph(modules)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(g.modules) != 3 {
			t.Errorf("modules count = %d, want 3", len(g.modules))
		}

		// Forward deps.
		if !equalStrings(g.dependencies["b"], []string{"a"}) {
			t.Errorf("dep[b] = %v", g.dependencies["b"])
		}
		if !equalStrings(g.dependencies["c"], []string{"a", "b"}) {
			t.Errorf("dep[c] = %v", g.dependencies["c"])
		}

		// Reverse deps: who depends on "a"? -> b and c.
		gotRevA := append([]string(nil), g.reverseDeps["a"]...)
		sort.Strings(gotRevA)
		if !equalStrings(gotRevA, []string{"b", "c"}) {
			t.Errorf("reverseDeps[a] = %v", gotRevA)
		}
		// "b" is depended on only by c.
		if !equalStrings(g.reverseDeps["b"], []string{"c"}) {
			t.Errorf("reverseDeps[b] = %v", g.reverseDeps["b"])
		}
	})

	t.Run("missing dependency returns error", func(t *testing.T) {
		modules := []*config.ModuleConfig{
			{Name: "a", Dependencies: []string{"ghost"}},
		}
		_, err := buildModuleGraph(modules)
		if err == nil {
			t.Fatal("expected error for missing dependency")
		}
		if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "a") {
			t.Errorf("error should mention 'ghost' and 'a': %v", err)
		}
	})
}

func TestTopologicalSortLevels(t *testing.T) {
	t.Run("orders by dependency depth (multi-level)", func(t *testing.T) {
		modules := []*config.ModuleConfig{
			{Name: "leaf1"},
			{Name: "leaf2"},
			{Name: "mid", Dependencies: []string{"leaf1", "leaf2"}},
			{Name: "top", Dependencies: []string{"mid"}},
		}
		g, err := buildModuleGraph(modules)
		if err != nil {
			t.Fatalf("graph error: %v", err)
		}
		levels, err := topologicalSortLevels(g)
		if err != nil {
			t.Fatalf("sort error: %v", err)
		}
		if len(levels) != 3 {
			t.Fatalf("want 3 levels, got %d (%v)", len(levels), levelNames(levels))
		}
		level0 := names(levels[0])
		sort.Strings(level0)
		if !equalStrings(level0, []string{"leaf1", "leaf2"}) {
			t.Errorf("level 0 = %v, want [leaf1 leaf2]", level0)
		}
		if !equalStrings(names(levels[1]), []string{"mid"}) {
			t.Errorf("level 1 = %v, want [mid]", names(levels[1]))
		}
		if !equalStrings(names(levels[2]), []string{"top"}) {
			t.Errorf("level 2 = %v, want [top]", names(levels[2]))
		}
	})

	t.Run("flat list -> single level", func(t *testing.T) {
		modules := []*config.ModuleConfig{
			{Name: "a"}, {Name: "b"}, {Name: "c"},
		}
		g, _ := buildModuleGraph(modules)
		levels, err := topologicalSortLevels(g)
		if err != nil {
			t.Fatalf("sort error: %v", err)
		}
		if len(levels) != 1 || len(levels[0]) != 3 {
			t.Errorf("expected 1 level of 3 modules, got %d levels (%v)", len(levels), levelNames(levels))
		}
	})

	t.Run("cycle detection returns error listing remaining modules", func(t *testing.T) {
		// Cycle: a -> b -> a
		modules := []*config.ModuleConfig{
			{Name: "a", Dependencies: []string{"b"}},
			{Name: "b", Dependencies: []string{"a"}},
		}
		g, err := buildModuleGraph(modules)
		if err != nil {
			t.Fatalf("graph error: %v", err)
		}
		_, err = topologicalSortLevels(g)
		if err == nil {
			t.Fatal("expected error for cycle")
		}
		if !strings.Contains(err.Error(), "circular dependency") {
			t.Errorf("want 'circular dependency' in error: %v", err)
		}
		// Must list both remaining modules.
		if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
			t.Errorf("error should list cycle members: %v", err)
		}
	})

	t.Run("empty graph returns no levels", func(t *testing.T) {
		g, _ := buildModuleGraph(nil)
		levels, err := topologicalSortLevels(g)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(levels) != 0 {
			t.Errorf("want 0 levels, got %d", len(levels))
		}
	})
}

func TestIsWebhookConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection refused", errors.New("dial: connection refused"), true},
		{"failed calling webhook", errors.New("failed calling webhook foo.bar"), true},
		{"webhook + timeout combo", errors.New("Internal error: webhook handler request timeout"), true},
		{"plain webhook word only", errors.New("webhook validating CR"), false},
		{"unrelated error", errors.New("oops"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isWebhookConnectionError(tc.err)
			if got != tc.want {
				t.Fatalf("isWebhookConnectionError(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// helpers ---------------------------------------------------------------

func names(modules []*config.ModuleConfig) []string {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		out = append(out, m.Name)
	}
	return out
}

func levelNames(levels [][]*config.ModuleConfig) [][]string {
	out := make([][]string, len(levels))
	for i, lvl := range levels {
		out[i] = names(lvl)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
