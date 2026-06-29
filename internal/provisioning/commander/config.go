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

import "time"

// Config holds the Deckhouse Commander provider settings, populated from
// environment variables. Unlike the legacy COMMANDER_* knobs read by
// pkg/cluster, the provider uses the E2E_COMMANDER_* prefix so it lines up with
// the other provider configs (E2E_DVP_BASE_CLUSTER_*) and the CI secret naming.
type Config struct {
	// URL and Token authenticate against the Commander API.
	URL   string `env:"E2E_COMMANDER_URL,required"`
	Token string `env:"E2E_COMMANDER_TOKEN,required"`

	// ClusterName is the name of the cluster to create (and later remove). It
	// must be stable across the bootstrap and teardown processes, so — unlike
	// the legacy Ginkgo path — it is NOT randomized here. The CI pipeline passes
	// a per-PR name (e.g. e2e-<module>-pr<N>).
	ClusterName string `env:"E2E_COMMANDER_CLUSTER_NAME" envDefault:"e2e-test-cluster"`

	// TemplateName selects the cluster template the new cluster is created from.
	// TemplateVersion optionally pins a specific version (by name or ID); when
	// empty the template's current version (or first available) is used.
	TemplateName    string `env:"E2E_COMMANDER_TEMPLATE_NAME,required"`
	TemplateVersion string `env:"E2E_COMMANDER_TEMPLATE_VERSION"`

	// RegistryName, when set, is resolved to a registry_id passed to the create
	// request (lets the cluster pull from a specific registry).
	RegistryName string `env:"E2E_COMMANDER_REGISTRY_NAME"`

	// InputValues is an optional JSON object of template input parameters merged
	// into the create request (e.g. releaseChannel, kubeVersion). The provider
	// always sets "prefix" to ClusterName on top.
	InputValues string `env:"E2E_COMMANDER_VALUES"`

	// Auth / transport tuning.
	AuthMethod            string `env:"E2E_COMMANDER_AUTH_METHOD" envDefault:"x-auth-token"`
	AuthUser              string `env:"E2E_COMMANDER_AUTH_USER"`
	APIPrefix             string `env:"E2E_COMMANDER_API_PREFIX" envDefault:"/api/v1"`
	InsecureSkipTLSVerify bool   `env:"E2E_COMMANDER_INSECURE_SKIP_TLS_VERIFY" envDefault:"false"`
	CACertPath            string `env:"E2E_COMMANDER_CA_CERT"`

	// WaitTimeout bounds the wait for the created cluster to reach Ready.
	WaitTimeout time.Duration `env:"E2E_COMMANDER_WAIT_TIMEOUT" envDefault:"30m"`
}
