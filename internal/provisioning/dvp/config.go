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

type Config struct {
	SSHUser    string `env:"E2E_DVP_SSH_USER,required"`
	SSHHost    string `env:"E2E_DVP_SSH_HOST,required"`
	SSHKeyPath string `env:"E2E_DVP_SSH_KEY_PATH,required"`

	SSHJumpHost    string `env:"E2E_DVP_SSH_JUMP_HOST"`
	SSHJumpUser    string `env:"E2E_DVP_SSH_JUMP_USER"`
	SSHJumpKeyPath string `env:"E2E_DVP_SSH_JUMP_KEY_PATH"`
}

// baseEndpoint builds the SSH endpoint for the DVP base cluster control-plane,
// routing through the jump host when one is configured.
func (c *Config) baseEndpoint() sshEndpoint {
	ep := sshEndpoint{User: c.SSHUser, Host: c.SSHHost, KeyPath: c.SSHKeyPath}
	if c.SSHJumpHost == "" {
		return ep
	}

	jump := sshEndpoint{User: c.SSHJumpUser, Host: c.SSHJumpHost, KeyPath: c.SSHJumpKeyPath}
	if jump.User == "" {
		jump.User = c.SSHUser
	}
	if jump.KeyPath == "" {
		jump.KeyPath = c.SSHKeyPath
	}
	ep.Jump = &jump
	return ep
}
