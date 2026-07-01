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

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// apiServerRemotePort is the node-local kube-api-proxy port the fetched
// kubeconfig points at; the connector tunnels to it over SSH.
const apiServerRemotePort = 6445

var (
	ErrSSHKeySource       = errors.New("exactly one of E2E_COMMANDER_SSH_PRIVATE_KEY_PATH or E2E_COMMANDER_SSH_PRIVATE_KEY must be set")
	ErrJumpHostIncomplete = errors.New("jump host requires both E2E_COMMANDER_SSH_JUMP_HOST and E2E_COMMANDER_SSH_JUMP_USER")
	ErrJumpKeySource      = errors.New("at most one of E2E_COMMANDER_SSH_JUMP_PRIVATE_KEY_PATH or E2E_COMMANDER_SSH_JUMP_PRIVATE_KEY may be set (defaults to the master key)")
)

// Config holds the Deckhouse Commander provider settings, populated from
// environment variables. The SSH fields mirror the DVP provider's shape
// (path-or-inline credentials, optional jump host) so the two providers can
// eventually share a neutral env-var vocabulary; only the master host differs —
// it is resolved from the Commander connection info rather than configured.
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
	// Required only for Bootstrap (create); the connect path (enable-modules /
	// suite) does not need it, so it is validated in createCluster rather than
	// via a struct tag. TemplateVersion optionally pins a specific version (by
	// name or ID); when empty the template's current version (or first) is used.
	TemplateName    string `env:"E2E_COMMANDER_TEMPLATE_NAME"`
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

	// SSH* reach the master over SSH (via a jump host when configured) to fetch
	// the kubeconfig and open the API tunnel. The master host comes from the
	// Commander connection info; SSHUser overrides the Commander-reported user.
	// Keys are supplied either by path (local runs) or inline content (CI).
	SSHUser       string `env:"E2E_COMMANDER_SSH_USER"`
	SSHPassphrase string `env:"E2E_COMMANDER_SSH_PASSPHRASE"`
	SSHKeyPath    string `env:"E2E_COMMANDER_SSH_PRIVATE_KEY_PATH"`
	SSHKeyContent string `env:"E2E_COMMANDER_SSH_PRIVATE_KEY"`

	SSHJumpHost       string `env:"E2E_COMMANDER_SSH_JUMP_HOST"`
	SSHJumpUser       string `env:"E2E_COMMANDER_SSH_JUMP_USER"`
	SSHJumpPassphrase string `env:"E2E_COMMANDER_SSH_JUMP_KEY_PASSPHRASE"`
	SSHJumpKeyPath    string `env:"E2E_COMMANDER_SSH_JUMP_PRIVATE_KEY_PATH"`
	SSHJumpKeyContent string `env:"E2E_COMMANDER_SSH_JUMP_PRIVATE_KEY"`
}

// Credentials holds the resolved (inline or file-loaded) SSH key material.
type Credentials struct {
	SSHKey  []byte
	JumpKey []byte
}

// LoadConfig parses the Commander config from the given environment and
// validates it.
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

// Validate checks the SSH credential sources are unambiguous.
func (c *Config) Validate() error {
	var errs []error
	if !exactlyOne(c.SSHKeyPath != "", c.SSHKeyContent != "") {
		errs = append(errs, ErrSSHKeySource)
	}
	if c.jumpHostMentioned() && !c.JumpHostConfigured() {
		errs = append(errs, ErrJumpHostIncomplete)
	}
	if c.SSHJumpKeyPath != "" && c.SSHJumpKeyContent != "" {
		errs = append(errs, ErrJumpKeySource)
	}
	return errors.Join(errs...)
}

// JumpHostConfigured reports whether a jump-host hop is configured. The jump key
// is optional — it defaults to the master key (a bastion commonly shares it).
func (c *Config) JumpHostConfigured() bool {
	return c.SSHJumpHost != "" && c.SSHJumpUser != ""
}

func (c *Config) jumpHostMentioned() bool {
	return c.SSHJumpHost != "" ||
		c.SSHJumpUser != "" ||
		c.SSHJumpKeyPath != "" ||
		c.SSHJumpKeyContent != ""
}

// Resolve loads the SSH key material from inline content or files. When a jump
// host is configured without its own key, the master key is reused.
func (c *Config) Resolve() (Credentials, error) {
	var creds Credentials
	var err error

	creds.SSHKey, err = resolveBytes(c.SSHKeyPath, c.SSHKeyContent)
	if err != nil {
		return Credentials{}, fmt.Errorf("resolving ssh private key: %w", err)
	}

	if c.JumpHostConfigured() {
		if c.SSHJumpKeyPath != "" || c.SSHJumpKeyContent != "" {
			creds.JumpKey, err = resolveBytes(c.SSHJumpKeyPath, c.SSHJumpKeyContent)
			if err != nil {
				return Credentials{}, fmt.Errorf("resolving jump ssh private key: %w", err)
			}
		} else {
			creds.JumpKey = creds.SSHKey
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
