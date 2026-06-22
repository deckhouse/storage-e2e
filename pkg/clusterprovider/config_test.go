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
	"os"
	"testing"
)

const (
	clusterProviderEnv       = "E2E_TEST_CLUSTER_PROVIDER"
	clusterConfigYamlPathEnv = "E2E_CLUSTER_CONFIG_YAML_PATH"
)

func unsetClusterProviderEnv(t *testing.T) {
	t.Helper()
	orig, had := os.LookupEnv(clusterProviderEnv)
	if err := os.Unsetenv(clusterProviderEnv); err != nil {
		t.Fatalf("failed to unset %s: %v", clusterProviderEnv, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(clusterProviderEnv, orig)
			return
		}
		_ = os.Unsetenv(clusterProviderEnv)
	})
}

func TestNew_ParsesProvider(t *testing.T) {
	t.Setenv(clusterProviderEnv, ModeDVP)
	t.Setenv(clusterConfigYamlPathEnv, "/tmp/cluster-config.yaml")

	cfg, err := NewClusterConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ClusterProvider != ModeDVP {
		t.Errorf("ClusterProvider = %q, want %q", cfg.ClusterProvider, ModeDVP)
	}
}

func TestNew_RequiredProviderMissing(t *testing.T) {
	unsetClusterProviderEnv(t)

	cfg, err := NewClusterConfig()
	if err == nil {
		t.Fatal("expected error when required env var is missing")
	}
	if cfg != nil {
		t.Errorf("expected nil config on error, got %+v", cfg)
	}
}

func TestNew_ProviderValues(t *testing.T) {
	tests := []struct {
		name  string
		value ProviderMode
	}{
		{name: "dvp", value: ModeDVP},
		{name: "commander", value: ModeCommander},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(clusterProviderEnv, tt.value.String())
			t.Setenv(clusterConfigYamlPathEnv, "/tmp/cluster-config.yaml")

			cfg, err := NewClusterConfig()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ClusterProvider != tt.value {
				t.Errorf("ClusterProvider = %q, want %q", cfg.ClusterProvider, tt.value)
			}
		})
	}
}
