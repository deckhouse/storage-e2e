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

func TestValidateModulePullOverrides_RejectsEnvPlaceholder(t *testing.T) {
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "csi-ceph", ModulePullOverride: "${MODULE_IMAGE_TAG}"},
			},
		},
	}
	err := ValidateModulePullOverrides(def)
	if err == nil {
		t.Fatal("expected error for ${VAR} placeholder")
	}
	if !strings.Contains(err.Error(), "csi-ceph") {
		t.Errorf("error should mention module name, got: %v", err)
	}
}

func TestValidateModulePullOverrides_AllowsLiteralTags(t *testing.T) {
	def := &ClusterDefinition{
		DKPParameters: DKPParameters{
			Modules: []*ModuleConfig{
				{Name: "csi-ceph", ModulePullOverride: "pr131"},
				{Name: "sds-elastic", ModulePullOverride: "mr55"},
			},
		},
	}
	if err := ValidateModulePullOverrides(def); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
