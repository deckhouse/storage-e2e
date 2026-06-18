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

package config

import (
	"fmt"
	"os"

	"github.com/hashicorp/go-multierror"
)

type ClusterConfig struct {
	SSHUser       string `env:"E2E_DVP_BASE_CLUSTER_SSH_USER"`
	SSHHost       string `env:"E2E_DVP_BASE_CLUSTER_SSH_HOST"`
	SSHKeyPath    string `env:"E2E_DVP_BASE_CLUSTER_SSH_KEY_PATH"`
	SSHPassphrase string `env:"E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE"`

	SSHJumpHost    string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST"`
	SSHJumpUser    string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_USER"`
	SSHJumpKeyPath string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_KEY_PATH"`

	KubeConfigPath string `env:"E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH"`

	Namespace string `env:"E2E_DVP_BASE_CLUSTER_NAMESPACE" envDefault:"e2e-test-cluster"`

	StorageClass string `env:"E2E_DVP_BASE_CLUSTER_STORAGE_CLASS"`
}

func (c *ClusterConfig) Validate() *multierror.Error {
	var errors *multierror.Error
	if c.SSHUser == "" {
		errors = multierror.Append(errors, fmt.Errorf("E2E_DVP_BASE_CLUSTER_SSH_USER required"))
	}

	if c.SSHHost == "" {
		errors = multierror.Append(errors, fmt.Errorf("E2E_DVP_BASE_CLUSTER_SSH_HOST required"))
	}

	if c.SSHKeyPath == "" {
		errors = multierror.Append(errors, fmt.Errorf("E2E_DVP_BASE_CLUSTER_SSH_KEY_PATH required"))
	}

	if c.KubeConfigPath == "" {
		errors = multierror.Append(errors, fmt.Errorf("E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH required"))
	}

	return errors
}

//func (c *ClusterConfig) sshPublicKey() (string, error) {
//	path, err := dvp.expandUserPath(c.SSHKeyPath + ".pub")
//	if err != nil {
//		return "", err
//	}
//	raw, err := os.ReadFile(path)
//	if err != nil {
//		return "", fmt.Errorf("read SSH public key %q: %w", path, err)
//	}
//	key := strings.TrimSpace(string(raw))
//	if key == "" {
//		return "", fmt.Errorf("SSH public key %q is empty", path)
//	}
//	return key, nil
//}

func (c *ClusterConfig) SetPassphrase() error {
	if c.SSHPassphrase == "" {
		return nil
	}
	if err := os.Setenv("SSH_PASSPHRASE", c.SSHPassphrase); err != nil {
		return fmt.Errorf("failed to set SSH_PASSPHRASE: %w", err)
	}
	return nil
}

//func BaseEndpoint() dvp.sshEndpoint {
//	ep := dvp.sshEndpoint{User: c.SSHUser, Host: c.SSHHost, KeyPath: c.SSHKeyPath}
//	if c.SSHJumpHost == "" {
//		return ep
//	}
//
//	jump := dvp.sshEndpoint{User: c.SSHJumpUser, Host: c.SSHJumpHost, KeyPath: c.SSHJumpKeyPath}
//	if jump.User == "" {
//		jump.User = c.SSHUser
//	}
//	if jump.KeyPath == "" {
//		jump.KeyPath = c.SSHKeyPath
//	}
//	ep.Jump = &jump
//	return ep
//}
