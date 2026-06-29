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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env/v11"
)

const apiServerRemotePort = 6445

var (
	ErrSSHUserRequired    = errors.New("E2E_DVP_BASE_CLUSTER_SSH_USER is required")
	ErrSSHHostRequired    = errors.New("E2E_DVP_BASE_CLUSTER_SSH_HOST is required")
	ErrKubeconfigSource   = errors.New("exactly one of E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH or E2E_DVP_BASE_CLUSTER_KUBECONFIG must be set")
	ErrSSHKeySource       = errors.New("exactly one of E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY_PATH or E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY must be set")
	ErrJumpHostIncomplete = errors.New("jump host requires E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST, E2E_DVP_BASE_CLUSTER_SSH_JUMP_USER and exactly one of E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY_PATH or E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY")
	ErrEmptyKubeconfig    = errors.New("kubeconfig is empty")
)

type Config struct {
	SSHUser       string `env:"E2E_DVP_BASE_CLUSTER_SSH_USER,required"`
	SSHHost       string `env:"E2E_DVP_BASE_CLUSTER_SSH_HOST,required"`
	SSHPassphrase string `env:"E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE"`

	SSHKeyPath    string `env:"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY_PATH"`
	SSHKeyContent string `env:"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY"`

	SSHJumpHost       string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST"`
	SSHJumpUser       string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_USER"`
	SSHJumpPassphrase string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_KEY_PASSPHRASE"`
	SSHJumpKeyPath    string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY_PATH"`
	SSHJumpKeyContent string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY"`

	KubeConfigPath    string `env:"E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH"`
	KubeConfigContent string `env:"E2E_DVP_BASE_CLUSTER_KUBECONFIG"`

	Namespace string `env:"E2E_DVP_BASE_CLUSTER_NAMESPACE" envDefault:"e2e-test-cluster"`

	StorageClass string `env:"E2E_DVP_BASE_CLUSTER_STORAGE_CLASS"`

	VMClassName string `env:"E2E_DVP_BASE_CLUSTER_VM_CLASS" envDefault:"generic"`

	DefaultVMClassName string `env:"E2E_DVP_BASE_CLUSTER_DEFAULT_VM_CLASS" envDefault:"generic"`
}

type Credentials struct {
	Kubeconfig []byte
	SSHKey     []byte
	JumpKey    []byte
}

func LoadConfig(environ map[string]string) (*Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Environment: environ})
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []error

	if c.SSHUser == "" {
		errs = append(errs, ErrSSHUserRequired)
	}
	if c.SSHHost == "" {
		errs = append(errs, ErrSSHHostRequired)
	}
	if !exactlyOne(c.KubeConfigPath != "", c.KubeConfigContent != "") {
		errs = append(errs, ErrKubeconfigSource)
	}
	if !exactlyOne(c.SSHKeyPath != "", c.SSHKeyContent != "") {
		errs = append(errs, ErrSSHKeySource)
	}
	if c.jumpHostMentioned() && !c.JumpHostConfigured() {
		errs = append(errs, ErrJumpHostIncomplete)
	}

	return errors.Join(errs...)
}

func (c *Config) JumpHostConfigured() bool {
	return c.SSHJumpHost != "" &&
		c.SSHJumpUser != "" &&
		exactlyOne(c.SSHJumpKeyPath != "", c.SSHJumpKeyContent != "")
}

func (c *Config) jumpHostMentioned() bool {
	return c.SSHJumpHost != "" ||
		c.SSHJumpUser != "" ||
		c.SSHJumpKeyPath != "" ||
		c.SSHJumpKeyContent != ""
}

func (c *Config) Resolve() (Credentials, error) {
	var creds Credentials
	var err error

	creds.Kubeconfig, err = resolveBytes(c.KubeConfigPath, c.KubeConfigContent)
	if err != nil {
		return Credentials{}, fmt.Errorf("resolving kubeconfig: %w", err)
	}
	if strings.TrimSpace(string(creds.Kubeconfig)) == "" {
		return Credentials{}, ErrEmptyKubeconfig
	}

	creds.SSHKey, err = resolveBytes(c.SSHKeyPath, c.SSHKeyContent)
	if err != nil {
		return Credentials{}, fmt.Errorf("resolving ssh private key: %w", err)
	}

	if c.JumpHostConfigured() {
		creds.JumpKey, err = resolveBytes(c.SSHJumpKeyPath, c.SSHJumpKeyContent)
		if err != nil {
			return Credentials{}, fmt.Errorf("resolving jump ssh private key: %w", err)
		}
	}

	return creds, nil
}

func resolveBytes(path, content string) ([]byte, error) {
	if content != "" {
		return []byte(content), nil
	}
	expanded, err := expandUserPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", expanded, err)
	}
	return raw, nil
}

func expandUserPath(path string) (string, error) {
	expanded := os.ExpandEnv(path)
	if !strings.HasPrefix(expanded, "~") {
		return expanded, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for %q: %w", path, err)
	}
	if expanded == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(expanded, "~/")), nil
}

func exactlyOne(a, b bool) bool { return a != b }
