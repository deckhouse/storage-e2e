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

type EnvLookup func(name string) (value string, ok bool)

var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

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
				return ""
			}
			return value
		})

		if strings.Contains(envRefPattern.ReplaceAllString(original, ""), "${") {
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
