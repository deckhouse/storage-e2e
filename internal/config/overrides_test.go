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
	"os"
	"strings"
	"testing"
)

func TestExpandEnvInModulePullOverride_NoPlaceholder(t *testing.T) {
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "snapshot-controller", ModulePullOverride: "main"},
				{Name: "csi-ceph", ModulePullOverride: ""},
				{Name: "sds-elastic"},
			},
		},
	}
	if err := ExpandEnvInModulePullOverride(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "main" {
		t.Errorf("snapshot-controller: got %q, want %q", got, "main")
	}
	if got := def.DKPParameters.Modules[1].ModulePullOverride; got != "" {
		t.Errorf("csi-ceph: got %q, want empty", got)
	}
	if got := def.DKPParameters.Modules[2].ModulePullOverride; got != "" {
		t.Errorf("sds-elastic: got %q, want empty", got)
	}
}

func TestExpandEnvInModulePullOverride_Expands(t *testing.T) {
	t.Setenv("MODULE_IMAGE_TAG", "pr131")
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "csi-ceph", ModulePullOverride: "${MODULE_IMAGE_TAG}"},
			},
		},
	}
	if err := ExpandEnvInModulePullOverride(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "pr131" {
		t.Errorf("got %q, want %q", got, "pr131")
	}
}

func TestExpandEnvInModulePullOverride_MissingEnvFails(t *testing.T) {
	// Use t.Setenv to register cleanup that restores the original value (if
	// any) after the test, then os.Unsetenv to actually drop it for this run.
	const name = "MISSING_TAG_FOR_TEST"
	t.Setenv(name, "anything")
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("os.Unsetenv: %v", err)
	}

	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "snapshot-controller", ModulePullOverride: "main"},
				{Name: "csi-ceph", ModulePullOverride: "${" + name + "}"},
			},
		},
	}
	err := ExpandEnvInModulePullOverride(def)
	if err == nil {
		t.Fatalf("expected error for missing env, got nil")
	}
	if !strings.Contains(err.Error(), "csi-ceph") {
		t.Errorf("error should mention offending module name, got: %v", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error should mention env var name %q, got: %v", name, err)
	}
}

func TestExpandEnvInModulePullOverride_PerModuleEnvs(t *testing.T) {
	t.Setenv("CSI_CEPH_TAG", "pr131")
	t.Setenv("SDS_ELASTIC_TAG", "mr41")

	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "csi-ceph", ModulePullOverride: "${CSI_CEPH_TAG}"},
				{Name: "sds-elastic", ModulePullOverride: "${SDS_ELASTIC_TAG}"},
				{Name: "snapshot-controller", ModulePullOverride: "main"},
			},
		},
	}
	if err := ExpandEnvInModulePullOverride(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "pr131" {
		t.Errorf("csi-ceph: got %q, want %q", got, "pr131")
	}
	if got := def.DKPParameters.Modules[1].ModulePullOverride; got != "mr41" {
		t.Errorf("sds-elastic: got %q, want %q", got, "mr41")
	}
	if got := def.DKPParameters.Modules[2].ModulePullOverride; got != "main" {
		t.Errorf("snapshot-controller: got %q, want %q", got, "main")
	}
}

func TestExpandEnvInModulePullOverride_MultiplePlaceholdersInOneString(t *testing.T) {
	t.Setenv("PREFIX", "branch")
	t.Setenv("NAME", "ms-crc")
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "csi-ceph", ModulePullOverride: "${PREFIX}-${NAME}"},
			},
		},
	}
	if err := ExpandEnvInModulePullOverride(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := def.DKPParameters.Modules[0].ModulePullOverride; got != "branch-ms-crc" {
		t.Errorf("got %q, want %q", got, "branch-ms-crc")
	}
}

func TestExpandEnvInModulePullOverride_NilModuleSliceEntry(t *testing.T) {
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{nil, {Name: "csi-ceph", ModulePullOverride: "main"}},
		},
	}
	if err := ExpandEnvInModulePullOverride(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
