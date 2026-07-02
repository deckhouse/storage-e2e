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

package dvp

import (
	"errors"
	"testing"
)

func validConfig() Config {
	return Config{
		SSHUser:           "user",
		SSHHost:           "host",
		SSHKeyContent:     "ssh-key",
		KubeConfigContent: "kubeconfig",
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr []error
	}{
		{
			name:    "valid minimal config",
			mutate:  func(c *Config) {},
			wantErr: nil,
		},
		{
			name:    "missing ssh user",
			mutate:  func(c *Config) { c.SSHUser = "" },
			wantErr: []error{ErrSSHUserRequired},
		},
		{
			name:    "missing ssh host",
			mutate:  func(c *Config) { c.SSHHost = "" },
			wantErr: []error{ErrSSHHostRequired},
		},
		{
			name:    "kubeconfig both path and content",
			mutate:  func(c *Config) { c.KubeConfigPath = "/tmp/kc" },
			wantErr: []error{ErrKubeconfigSource},
		},
		{
			name:    "kubeconfig neither path nor content",
			mutate:  func(c *Config) { c.KubeConfigContent = "" },
			wantErr: []error{ErrKubeconfigSource},
		},
		{
			name:    "ssh key both path and content",
			mutate:  func(c *Config) { c.SSHKeyPath = "/tmp/key" },
			wantErr: []error{ErrSSHKeySource},
		},
		{
			name:    "ssh key neither path nor content",
			mutate:  func(c *Config) { c.SSHKeyContent = "" },
			wantErr: []error{ErrSSHKeySource},
		},
		{
			name:    "partial jump host: only host",
			mutate:  func(c *Config) { c.SSHJumpHost = "jump" },
			wantErr: []error{ErrJumpHostIncomplete},
		},
		{
			name:    "partial jump host: only jump key content",
			mutate:  func(c *Config) { c.SSHJumpKeyContent = "jump-key" },
			wantErr: []error{ErrJumpHostIncomplete},
		},
		{
			name: "fully configured jump host",
			mutate: func(c *Config) {
				c.SSHJumpHost = "jump"
				c.SSHJumpUser = "jumpuser"
				c.SSHJumpKeyContent = "jump-key"
			},
			wantErr: nil,
		},
		{
			name: "multiple violations are joined",
			mutate: func(c *Config) {
				c.SSHUser = ""
				c.SSHHost = ""
			},
			wantErr: []error{ErrSSHUserRequired, ErrSSHHostRequired},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want errors %v", tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("Validate() = %v, want errors.Is %v", err, want)
				}
			}
		})
	}
}

func TestValidateForBootstrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr []error
	}{
		{
			name: "license and dockercfg present",
			cfg:  Config{DKPLicenseKey: "lic", RegistryDockerCfg: "cfg"},
		},
		{
			name:    "missing license",
			cfg:     Config{RegistryDockerCfg: "cfg"},
			wantErr: []error{ErrDKPLicenseKeyRequired},
		},
		{
			name:    "missing dockercfg",
			cfg:     Config{DKPLicenseKey: "lic"},
			wantErr: []error{ErrRegistryDockerCfgRequired},
		},
		{
			name:    "both missing are joined",
			cfg:     Config{},
			wantErr: []error{ErrDKPLicenseKeyRequired, ErrRegistryDockerCfgRequired},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.ValidateForBootstrap()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("ValidateForBootstrap() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateForBootstrap() = nil, want errors %v", tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("ValidateForBootstrap() = %v, want errors.Is %v", err, want)
				}
			}
		})
	}
}

func TestJumpHostConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "no jump fields", cfg: Config{}, want: false},
		{
			name: "host and user but no key",
			cfg:  Config{SSHJumpHost: "j", SSHJumpUser: "u"},
			want: false,
		},
		{
			name: "host, user and key content",
			cfg:  Config{SSHJumpHost: "j", SSHJumpUser: "u", SSHJumpKeyContent: "k"},
			want: true,
		},
		{
			name: "host, user and key path",
			cfg:  Config{SSHJumpHost: "j", SSHJumpUser: "u", SSHJumpKeyPath: "/k"},
			want: true,
		},
		{
			name: "both key sources is not configured",
			cfg:  Config{SSHJumpHost: "j", SSHJumpUser: "u", SSHJumpKeyPath: "/k", SSHJumpKeyContent: "k"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.JumpHostConfigured(); got != tc.want {
				t.Fatalf("JumpHostConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	t.Run("valid env map applies defaults", func(t *testing.T) {
		t.Parallel()
		environ := map[string]string{
			"E2E_DVP_BASE_CLUSTER_SSH_USER":        "user",
			"E2E_DVP_BASE_CLUSTER_SSH_HOST":        "host",
			"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY": "ssh-key",
			"E2E_DVP_BASE_CLUSTER_KUBECONFIG":      "kubeconfig",
		}
		cfg, err := LoadConfig(environ)
		if err != nil {
			t.Fatalf("LoadConfig() = %v, want nil", err)
		}
		if cfg.Namespace != "e2e-test-cluster" {
			t.Fatalf("Namespace = %q, want default e2e-test-cluster", cfg.Namespace)
		}
	})

	t.Run("missing required scalar fails", func(t *testing.T) {
		t.Parallel()
		environ := map[string]string{
			"E2E_DVP_BASE_CLUSTER_SSH_HOST":        "host",
			"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY": "ssh-key",
			"E2E_DVP_BASE_CLUSTER_KUBECONFIG":      "kubeconfig",
		}
		if _, err := LoadConfig(environ); err == nil {
			t.Fatalf("LoadConfig() = nil, want error for missing SSH_USER")
		}
	})

	t.Run("validation error surfaces", func(t *testing.T) {
		t.Parallel()
		environ := map[string]string{
			"E2E_DVP_BASE_CLUSTER_SSH_USER":        "user",
			"E2E_DVP_BASE_CLUSTER_SSH_HOST":        "host",
			"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY": "ssh-key",
			"E2E_DVP_BASE_CLUSTER_KUBECONFIG":      "kubeconfig",
			"E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH": "/tmp/kc",
		}
		if _, err := LoadConfig(environ); !errors.Is(err, ErrKubeconfigSource) {
			t.Fatalf("LoadConfig() = %v, want errors.Is ErrKubeconfigSource", err)
		}
	})
}
