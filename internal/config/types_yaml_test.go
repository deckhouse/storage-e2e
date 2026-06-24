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

	"gopkg.in/yaml.v3"
)

// pickKnownOSType returns one of the keys from OSTypeMap so we can build
// test YAML that is robust against changes to the canonical OS list.
func pickKnownOSType(t *testing.T) string {
	t.Helper()
	if len(OSTypeMap) == 0 {
		t.Fatal("OSTypeMap is empty; cannot build YAML fixtures")
	}
	// Prefer the well-known default if present for stable assertions.
	preferred := "Ubuntu 22.04 6.2.0-39-generic"
	if _, ok := OSTypeMap[preferred]; ok {
		return preferred
	}
	for k := range OSTypeMap {
		return k
	}
	return ""
}

func TestClusterNodeUnmarshalYAML(t *testing.T) {
	osName := pickKnownOSType(t)

	t.Run("VM node with valid fields", func(t *testing.T) {
		in := []byte(`
hostname: master-1
hostType: vm
osType: "` + osName + `"
cpu: 4
ram: 8
diskSize: 50
`)
		var node ClusterNode
		if err := yaml.Unmarshal(in, &node); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if node.Hostname != "master-1" {
			t.Errorf("Hostname=%q", node.Hostname)
		}
		if node.HostType != HostTypeVM {
			t.Errorf("HostType=%q", node.HostType)
		}
		if node.OSType.ImageURL == "" {
			t.Errorf("OSType.ImageURL not populated for %q", osName)
		}
		if node.CPU != 4 || node.RAM != 8 || node.DiskSize != 50 {
			t.Errorf("VM fields not parsed: %+v", node)
		}
	})

	t.Run("bare-metal node with role=setup", func(t *testing.T) {
		in := []byte(`
hostname: boot
hostType: bare-metal
osType: "` + osName + `"
role: setup
`)
		var node ClusterNode
		if err := yaml.Unmarshal(in, &node); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if node.HostType != HostTypeBareMetal {
			t.Errorf("HostType=%q", node.HostType)
		}
		if node.Role != ClusterRoleSetup {
			t.Errorf("Role=%q, want %q", node.Role, ClusterRoleSetup)
		}
	})

	t.Run("invalid hostType errors", func(t *testing.T) {
		in := []byte(`
hostname: x
hostType: container
osType: "` + osName + `"
`)
		var node ClusterNode
		err := yaml.Unmarshal(in, &node)
		if err == nil || !strings.Contains(err.Error(), "invalid hostType") {
			t.Errorf("want 'invalid hostType' error, got %v", err)
		}
	})

	t.Run("invalid role errors", func(t *testing.T) {
		in := []byte(`
hostname: x
hostType: vm
osType: "` + osName + `"
role: master
`)
		var node ClusterNode
		err := yaml.Unmarshal(in, &node)
		if err == nil || !strings.Contains(err.Error(), "invalid role") {
			t.Errorf("want 'invalid role' error, got %v", err)
		}
	})

	t.Run("unknown osType errors", func(t *testing.T) {
		in := []byte(`
hostname: x
hostType: vm
osType: "some-unknown-os 999"
`)
		var node ClusterNode
		err := yaml.Unmarshal(in, &node)
		if err == nil || !strings.Contains(err.Error(), "unknown osType") {
			t.Errorf("want 'unknown osType' error, got %v", err)
		}
	})
}

func TestClusterDefinitionUnmarshalYAML(t *testing.T) {
	osName := pickKnownOSType(t)

	t.Run("bare cluster definition (no wrapper key)", func(t *testing.T) {
		in := []byte(`
masters:
  - hostname: m1
    hostType: vm
    osType: "` + osName + `"
    cpu: 2
    ram: 4
    diskSize: 20
workers:
  - hostname: w1
    hostType: vm
    osType: "` + osName + `"
    cpu: 2
    ram: 4
    diskSize: 20
dkpParameters:
  kubernetesVersion: "1.30"
  registryRepo: dev-registry.deckhouse.io/sys/deckhouse-oss
`)
		var def ClusterDefinition
		if err := yaml.Unmarshal(in, &def); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(def.Masters) != 1 || def.Masters[0].Hostname != "m1" {
			t.Errorf("masters not parsed: %+v", def.Masters)
		}
		if len(def.Workers) != 1 || def.Workers[0].Hostname != "w1" {
			t.Errorf("workers not parsed: %+v", def.Workers)
		}
		if def.DKPParameters.KubernetesVersion != "1.30" {
			t.Errorf("KubernetesVersion=%q", def.DKPParameters.KubernetesVersion)
		}
	})

	t.Run("clusterDefinition wrapper key", func(t *testing.T) {
		in := []byte(`
clusterDefinition:
  masters:
    - hostname: m1
      hostType: vm
      osType: "` + osName + `"
      cpu: 1
      ram: 1
      diskSize: 10
  dkpParameters:
    registryRepo: dev-registry.deckhouse.io/sys/deckhouse-oss
`)
		var def ClusterDefinition
		if err := yaml.Unmarshal(in, &def); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(def.Masters) != 1 || def.Masters[0].Hostname != "m1" {
			t.Errorf("wrapper-key path didn't unwrap: %+v", def)
		}
		if def.DKPParameters.RegistryRepo != "dev-registry.deckhouse.io/sys/deckhouse-oss" {
			t.Errorf("registryRepo not parsed: %q", def.DKPParameters.RegistryRepo)
		}
	})

	t.Run("invalid nested node propagates error", func(t *testing.T) {
		in := []byte(`
masters:
  - hostname: m1
    hostType: container
    osType: "` + osName + `"
`)
		var def ClusterDefinition
		err := yaml.Unmarshal(in, &def)
		if err == nil || !strings.Contains(err.Error(), "invalid hostType") {
			t.Errorf("want 'invalid hostType' error, got %v", err)
		}
	})
}

func TestValidateModulePullOverrides_Extended(t *testing.T) {
	t.Run("nil module entries are ignored", func(t *testing.T) {
		def := &ClusterDefinition{
			DKPParameters: DKPParameters{
				Modules: []*ModuleConfig{
					nil,
					{Name: "ok", ModulePullOverride: "pr1"},
					nil,
				},
			},
		}
		if err := ValidateModulePullOverrides(def); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty override is allowed", func(t *testing.T) {
		def := &ClusterDefinition{
			DKPParameters: DKPParameters{
				Modules: []*ModuleConfig{
					{Name: "x", ModulePullOverride: ""},
				},
			},
		}
		if err := ValidateModulePullOverrides(def); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("no modules at all is allowed", func(t *testing.T) {
		def := &ClusterDefinition{}
		if err := ValidateModulePullOverrides(def); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
