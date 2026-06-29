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

package commander

import (
	"log/slog"
	"testing"

	commanderapi "github.com/deckhouse/storage-e2e/internal/kubernetes/commander"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

func TestNewCommanderProvider_ParsesEnvAndName(t *testing.T) {
	t.Setenv("E2E_COMMANDER_URL", "https://commander.example.com")
	t.Setenv("E2E_COMMANDER_TOKEN", "secret-token")
	t.Setenv("E2E_COMMANDER_CLUSTER_NAME", "e2e-sds-object-pr19")
	t.Setenv("E2E_COMMANDER_TEMPLATE_NAME", "default-template")

	p, err := NewCommanderProvider(slog.Default(), &clusterprovider.ClusterConfig{})
	if err != nil {
		t.Fatalf("NewCommanderProvider returned unexpected error: %v", err)
	}
	if got, want := p.Name(), clusterprovider.ModeCommander; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}

	cp, ok := p.(*commanderProvider)
	if !ok {
		t.Fatalf("expected *commanderProvider, got %T", p)
	}
	if cp.conf.ClusterName != "e2e-sds-object-pr19" {
		t.Errorf("ClusterName = %q, want %q", cp.conf.ClusterName, "e2e-sds-object-pr19")
	}
	if cp.conf.WaitTimeout.String() != "30m0s" {
		t.Errorf("WaitTimeout default = %q, want 30m0s", cp.conf.WaitTimeout)
	}
}

func TestNewCommanderProvider_RequiresURLAndToken(t *testing.T) {
	// Only the template name is set; URL/Token are required and missing.
	t.Setenv("E2E_COMMANDER_TEMPLATE_NAME", "default-template")

	if _, err := NewCommanderProvider(slog.Default(), &clusterprovider.ClusterConfig{}); err == nil {
		t.Fatal("expected error when E2E_COMMANDER_URL/E2E_COMMANDER_TOKEN are missing, got nil")
	}
}

func TestResolveTemplateVersionID(t *testing.T) {
	tmpl := &commanderapi.ClusterTemplateResponse{
		Name:                            "default-template",
		CurrentClusterTemplateVersionID: "ver-current",
		ClusterTemplateVersions: []commanderapi.TemplateVersionResponse{
			{ID: "ver-1", Name: "v1"},
			{ID: "ver-2", Name: "v2"},
		},
	}

	tests := []struct {
		name      string
		requested string
		want      string
		wantErr   bool
	}{
		{name: "explicit by name", requested: "v2", want: "ver-2"},
		{name: "explicit by id", requested: "ver-1", want: "ver-1"},
		{name: "default to current", requested: "", want: "ver-current"},
		{name: "unknown requested", requested: "v9", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplateVersionID(tmpl, tt.requested)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for requested=%q, got nil (id=%q)", tt.requested, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveTemplateVersionID(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

func TestResolveTemplateVersionID_FallbackOrder(t *testing.T) {
	// No current version set: falls back to the first available version.
	tmpl := &commanderapi.ClusterTemplateResponse{
		Name:     "t",
		Versions: []commanderapi.TemplateVersionResponse{{ID: "only", Name: "only"}},
	}
	got, err := resolveTemplateVersionID(tmpl, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "only" {
		t.Errorf("got %q, want %q", got, "only")
	}

	// No versions at all: error.
	empty := &commanderapi.ClusterTemplateResponse{Name: "empty"}
	if _, err := resolveTemplateVersionID(empty, ""); err == nil {
		t.Fatal("expected error for template with no versions, got nil")
	}
}

func TestBuildValues(t *testing.T) {
	p := &commanderProvider{conf: &Config{InputValues: `{"releaseChannel":"Stable"}`}}
	values, err := p.buildValues("my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["prefix"] != "my-cluster" {
		t.Errorf("prefix = %v, want my-cluster", values["prefix"])
	}
	if values["releaseChannel"] != "Stable" {
		t.Errorf("releaseChannel = %v, want Stable", values["releaseChannel"])
	}

	// Invalid JSON is reported as an error.
	bad := &commanderProvider{conf: &Config{InputValues: "{not-json"}}
	if _, err := bad.buildValues("x"); err == nil {
		t.Fatal("expected error for invalid E2E_COMMANDER_VALUES JSON, got nil")
	}
}
