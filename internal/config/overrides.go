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
	"regexp"
)

// envVarRefPattern matches ${NAME} placeholders. We accept only the braced
// form (no bare $NAME) to keep substitution intent explicit and avoid
// accidentally rewriting tags that legitimately contain a dollar sign.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnvInModulePullOverride expands ${VAR} references in each module's
// ModulePullOverride field. If a referenced env var is not set, returns an
// error pointing at the offending module so CI fails loudly instead of
// silently falling back to the "main" default in configureModulePullOverride.
//
// This lets test suites declare in YAML which modules should track a CI-built
// image without hard-coding any tag:
//
//	modules:
//	  - name: csi-ceph
//	    modulePullOverride: "${MODULE_IMAGE_TAG}"
//
// CI then sets MODULE_IMAGE_TAG=pr<N> (GitHub) or mr<N> (GitLab), and the
// resulting ModulePullOverride CR points at the right image without anyone
// editing the YAML per run.
//
// Use this hook right after yaml.Unmarshal of cluster_config.yml. Modules
// without any placeholder are left untouched.
func ExpandEnvInModulePullOverride(def *ClusterDefinition) error {
	for _, m := range def.DKPParameters.Modules {
		if m == nil || m.ModulePullOverride == "" {
			continue
		}
		matches := envVarRefPattern.FindAllStringSubmatch(m.ModulePullOverride, -1)
		if len(matches) == 0 {
			continue
		}
		for _, ms := range matches {
			if _, ok := os.LookupEnv(ms[1]); !ok {
				return fmt.Errorf(
					"module %q references env var ${%s} in modulePullOverride but it is not set",
					m.Name, ms[1],
				)
			}
		}
		m.ModulePullOverride = os.Expand(m.ModulePullOverride, os.Getenv)
	}
	return nil
}
