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
	"testing"
)

const clusterProviderEnv = "TEST_CLUSTER_PROVIDER"

// unsetClusterProviderEnv removes TEST_CLUSTER_PROVIDER for the duration of the
// test and restores its original value (set or unset) afterwards.
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
	t.Setenv(clusterProviderEnv, "static")

	cfg, err := New()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ClusterProvider != "static" {
		t.Errorf("ClusterProvider = %q, want %q", cfg.ClusterProvider, "static")
	}
}

func TestNew_RequiredProviderMissing(t *testing.T) {
	unsetClusterProviderEnv(t)

	cfg, err := New()
	if err == nil {
		t.Fatal("expected error when required env var is missing")
	}
	if cfg != nil {
		t.Errorf("expected nil config on error, got %+v", cfg)
	}
}

// env/v11 treats the `required` tag as a presence check: an empty value still
// counts as set, so parsing succeeds and the field is left empty.
func TestNew_EmptyProviderAccepted(t *testing.T) {
	t.Setenv(clusterProviderEnv, "")

	cfg, err := New()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ClusterProvider != "" {
		t.Errorf("ClusterProvider = %q, want empty string", cfg.ClusterProvider)
	}
}

func TestNew_ProviderValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "static", value: "static"},
		{name: "cloud-ephemeral", value: "CloudEphemeral"},
		{name: "with-spaces", value: "some provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(clusterProviderEnv, tt.value)

			cfg, err := New()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ClusterProvider != tt.value {
				t.Errorf("ClusterProvider = %q, want %q", cfg.ClusterProvider, tt.value)
			}
		})
	}
}
