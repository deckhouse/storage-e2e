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
	"regexp"
	"strings"
)

// EnvLookup resolves a variable by name and reports whether it is set. It
// mirrors os.LookupEnv so the standard environment can be injected directly,
// while tests can supply a map-backed source without mutating the process
// environment.
type EnvLookup func(name string) (value string, ok bool)

// envRefPattern matches a single ${NAME} reference. Only the braced form with
// shell-identifier characters is recognized, so a tag containing a bare '$'
// is never rewritten and substitution intent is always explicit. The same
// pattern drives both detection and replacement, so a reference can never be
// detected one way and substituted another.
var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ResolveModulePullOverrides substitutes ${NAME} references in every module's
// ModulePullOverride using lookup, letting CI point modules at per-build image
// tags without editing the YAML:
//
//	modules:
//	  - name: csi-ceph
//	    modulePullOverride: "${CSI_CEPH_TAG}"
//
// Each module may reference its own variable, and a field may contain several
// references. All problems across all modules are reported together:
//   - a reference to a variable that lookup does not provide, and
//   - a residual "${" left after substitution (a malformed reference such as
//     ${bad-name}), which guarantees no placeholder ever reaches the cluster.
//
// Modules without references are left untouched.
func ResolveModulePullOverrides(def *ClusterDefinition, lookup EnvLookup) error {
	if def == nil {
		return nil
	}

	var problems []string
	for _, m := range def.DKPParameters.Modules {
		if m == nil || m.ModulePullOverride == "" {
			continue
		}

		original := m.ModulePullOverride
		resolved := envRefPattern.ReplaceAllStringFunc(original, func(ref string) string {
			name := envRefPattern.FindStringSubmatch(ref)[1]
			value, ok := lookup(name)
			if !ok {
				problems = append(problems, fmt.Sprintf("module %q references unset ${%s}", m.Name, name))
				// Value is irrelevant: any problem aborts the whole load. Drop
				// the reference so it is not re-reported by the residual check.
				return ""
			}
			return value
		})

		if strings.Contains(resolved, "${") {
			problems = append(problems, fmt.Sprintf(
				"module %q has malformed modulePullOverride %q (only ${NAME} with letters, digits, and underscores is supported)",
				m.Name, original,
			))
			continue
		}

		m.ModulePullOverride = resolved
	}

	if len(problems) > 0 {
		return fmt.Errorf("modulePullOverride resolution failed: %s", strings.Join(problems, "; "))
	}
	return nil
}
