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
	"fmt"
	"os"
	"strings"
)

// ModulePullOverrideEnvSuffix is appended to the normalized module name to form
// the per-module env var that overrides modulePullOverride. For example module
// "sds-elastic" maps to "SDS_ELASTIC_MODULE_PULL_OVERRIDE".
const ModulePullOverrideEnvSuffix = "_MODULE_PULL_OVERRIDE"

// ModulePullOverrideDefaultTag is the image tag storage-e2e applies for dev
// registries when a module declares no modulePullOverride. It is surfaced here
// only so logs can name the effective default when the YAML value was empty.
const ModulePullOverrideDefaultTag = "main"

// EnvKeyForModulePullOverride returns the per-module env var name that overrides
// a module's modulePullOverride. The module name is upper-cased and every
// character invalid in a shell env var (anything outside [A-Z0-9]) is replaced
// with '_', so "sds-elastic" -> "SDS_ELASTIC_MODULE_PULL_OVERRIDE" and
// "csi-ceph" -> "CSI_CEPH_MODULE_PULL_OVERRIDE".
func EnvKeyForModulePullOverride(moduleName string) string {
	norm := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, moduleName)
	return norm + ModulePullOverrideEnvSuffix
}

// ModulePullOverrideChange records a single env-driven override of a module's
// modulePullOverride so the caller can log it explicitly.
type ModulePullOverrideChange struct {
	Module   string // module name
	EnvVar   string // env var that triggered the override
	FromYAML string // value declared in cluster_config.yml ("" when unset)
	ToEnv    string // value taken from the env var (the effective tag)
}

// LogLine renders a human-readable explanation of the override, naming BOTH the
// static cluster_config.yml value and the env var/tag that takes precedence, so
// the test output makes the source of the running image tag unambiguous.
func (c ModulePullOverrideChange) LogLine() string {
	if c.FromYAML == "" {
		return fmt.Sprintf(
			"modulePullOverride[%s]: cluster_config.yml sets no tag (effective default %q), but %s=%q is set — using tag %q for this test run",
			c.Module, ModulePullOverrideDefaultTag, c.EnvVar, c.ToEnv, c.ToEnv,
		)
	}
	return fmt.Sprintf(
		"modulePullOverride[%s]: cluster_config.yml pins tag %q, but %s=%q is set — using tag %q for this test run",
		c.Module, c.FromYAML, c.EnvVar, c.ToEnv, c.ToEnv,
	)
}

// ApplyModulePullOverrideEnv overrides each module's ModulePullOverride from its
// per-module env var (see EnvKeyForModulePullOverride) when that var is set and
// differs from the static value. This is the sanctioned, per-module channel for
// pointing the module-under-test at a CI image tag (pr<N>/mr<N>/main) without
// editing the committed cluster_config.yml — chosen over a single global
// MODULE_IMAGE_TAG so configs with several dev modules stay unambiguous.
//
// In-YAML ${VAR} templating remains unsupported (ValidateModulePullOverrides
// rejects it): the static file keeps literal, readable defaults and this env
// channel is applied right before validation. Returns the applied changes so
// the caller (which owns the logger) can report them; mutates def in place.
func ApplyModulePullOverrideEnv(def *ClusterDefinition) []ModulePullOverrideChange {
	if def == nil {
		return nil
	}
	var changes []ModulePullOverrideChange
	for _, m := range def.DKPParameters.Modules {
		if m == nil {
			continue
		}
		key := EnvKeyForModulePullOverride(m.Name)
		val := strings.TrimSpace(os.Getenv(key))
		if val == "" || val == m.ModulePullOverride {
			continue
		}
		changes = append(changes, ModulePullOverrideChange{
			Module:   m.Name,
			EnvVar:   key,
			FromYAML: m.ModulePullOverride,
			ToEnv:    val,
		})
		m.ModulePullOverride = val
	}
	return changes
}
