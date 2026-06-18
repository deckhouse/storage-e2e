/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package clusterprovider

import (
	"github.com/caarlos0/env/v11"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/config"
)

type ClusterConfig struct {
	ClusterProvider            ProviderMode `env:"E2E_TEST_CLUSTER_PROVIDER,required"`
	ClusterBootstrapConfigPath string       `env:"E2E_CLUSTER_CONFIG_YAML_PATH,required"`

	DVP config.ClusterConfig
}

func NewClusterConfig() (*ClusterConfig, error) {
	cfg := &ClusterConfig{}
	parseErr := env.Parse(cfg)
	if parseErr != nil {
		return nil, parseErr
	}

	return cfg, nil
}
