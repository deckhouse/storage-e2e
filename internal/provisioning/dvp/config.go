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

const apiServerRemotePort = 6445

type Config struct {
	SSHUser       string `env:"E2E_DVP_BASE_CLUSTER_SSH_USER,required"`
	SSHHost       string `env:"E2E_DVP_BASE_CLUSTER_SSH_HOST,required"`
	SSHKeyPath    string `env:"E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY_PATH,required"`
	SSHPassphrase string `env:"E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE"`

	SSHJumpHost       string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST"`
	SSHJumpUser       string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_USER"`
	SSHJumpKeyPath    string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY_PATH"`
	SSHJumpPassphrase string `env:"E2E_DVP_BASE_CLUSTER_SSH_JUMP_KEY_PASSPHRASE"`

	KubeConfigPath string `env:"E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH,required"`

	Namespace string `env:"E2E_DVP_BASE_CLUSTER_NAMESPACE" envDefault:"e2e-test-cluster"`

	StorageClass string `env:"E2E_DVP_BASE_CLUSTER_STORAGE_CLASS"`

	VMClassName string `env:"E2E_DVP_BASE_CLUSTER_VM_CLASS" envDefault:"generic"`

	DefaultVMClassName string `env:"E2E_DVP_BASE_CLUSTER_DEFAULT_VM_CLASS" envDefault:"generic"`
}

func (c *Config) HasJumpHost() bool {
	return c.SSHJumpUser != "" && c.SSHJumpHost != "" && c.SSHJumpKeyPath != ""
}
