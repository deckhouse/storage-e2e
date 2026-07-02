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

package config

import (
	"strings"
	"testing"
)

func TestEnvKeyForModulePullOverride(t *testing.T) {
	cases := map[string]string{
		"sds-elastic":           "SDS_ELASTIC_MODULE_PULL_OVERRIDE",
		"csi-ceph":              "CSI_CEPH_MODULE_PULL_OVERRIDE",
		"sds-node-configurator": "SDS_NODE_CONFIGURATOR_MODULE_PULL_OVERRIDE",
		"snapshot-controller":   "SNAPSHOT_CONTROLLER_MODULE_PULL_OVERRIDE",
	}
	for module, want := range cases {
		if got := EnvKeyForModulePullOverride(module); got != want {
			t.Errorf("EnvKeyForModulePullOverride(%q) = %q, want %q", module, got, want)
		}
	}
}

func newDef(modules ...*ModuleConfig) *ClusterDefinition {
	return &ClusterDefinition{DKPParameters: DKPParameters{Modules: modules}}
}

func TestApplyModulePullOverrideEnv_OverridesAndRecords(t *testing.T) {
	t.Setenv("SDS_ELASTIC_MODULE_PULL_OVERRIDE", "pr123")

	def := newDef(
		&ModuleConfig{Name: "sds-elastic", ModulePullOverride: "main"},
		&ModuleConfig{Name: "csi-ceph", ModulePullOverride: "main"},
	)

	changes := ApplyModulePullOverrideEnv(def)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "pr123" {
		t.Errorf("sds-elastic ModulePullOverride = %q, want pr123", got)
	}
	if got := def.DKPParameters.Modules[1].ModulePullOverride; got != "main" {
		t.Errorf("csi-ceph ModulePullOverride = %q, want main (untouched)", got)
	}

	ch := changes[0]
	if ch.Module != "sds-elastic" || ch.EnvVar != "SDS_ELASTIC_MODULE_PULL_OVERRIDE" ||
		ch.FromYAML != "main" || ch.ToEnv != "pr123" {
		t.Errorf("unexpected change: %+v", ch)
	}
	line := ch.LogLine()
	for _, want := range []string{"sds-elastic", `"main"`, "SDS_ELASTIC_MODULE_PULL_OVERRIDE", `"pr123"`} {
		if !strings.Contains(line, want) {
			t.Errorf("LogLine() = %q, missing %q", line, want)
		}
	}
}

func TestApplyModulePullOverrideEnv_NoEnvIsNoop(t *testing.T) {
	def := newDef(&ModuleConfig{Name: "sds-elastic", ModulePullOverride: "main"})
	if changes := ApplyModulePullOverrideEnv(def); len(changes) != 0 {
		t.Fatalf("expected no changes without env, got %+v", changes)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "main" {
		t.Errorf("ModulePullOverride = %q, want main (untouched)", got)
	}
}

func TestApplyModulePullOverrideEnv_EqualValueIsNoop(t *testing.T) {
	t.Setenv("SDS_ELASTIC_MODULE_PULL_OVERRIDE", "main")
	def := newDef(&ModuleConfig{Name: "sds-elastic", ModulePullOverride: "main"})
	if changes := ApplyModulePullOverrideEnv(def); len(changes) != 0 {
		t.Fatalf("expected no changes when env equals YAML, got %+v", changes)
	}
}

func TestApplyModulePullOverrideEnv_EmptyYAMLDefaultLogged(t *testing.T) {
	t.Setenv("SDS_ELASTIC_MODULE_PULL_OVERRIDE", "pr7")
	def := newDef(&ModuleConfig{Name: "sds-elastic"})

	changes := ApplyModulePullOverrideEnv(def)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "pr7" {
		t.Errorf("ModulePullOverride = %q, want pr7", got)
	}
	// With no static value the log should name the effective default tag.
	if line := changes[0].LogLine(); !strings.Contains(line, ModulePullOverrideDefaultTag) {
		t.Errorf("LogLine() = %q, expected to mention default %q", line, ModulePullOverrideDefaultTag)
	}
}
