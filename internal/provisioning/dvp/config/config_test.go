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
	"testing"
)

func TestConfigBaseEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want dvp.sshEndpoint
	}{
		{
			name: "no jump host",
			cfg: Config{
				SSHUser:    "deckhouse",
				SSHHost:    "10.0.0.1",
				SSHKeyPath: "/keys/id_rsa",
			},
			want: dvp.sshEndpoint{
				User:    "deckhouse",
				Host:    "10.0.0.1",
				KeyPath: "/keys/id_rsa",
				Jump:    nil,
			},
		},
		{
			name: "jump host fully specified",
			cfg: Config{
				SSHUser:        "deckhouse",
				SSHHost:        "10.0.0.1",
				SSHKeyPath:     "/keys/target",
				SSHJumpHost:    "jump.example.com",
				SSHJumpUser:    "jumper",
				SSHJumpKeyPath: "/keys/jump",
			},
			want: dvp.sshEndpoint{
				User:    "deckhouse",
				Host:    "10.0.0.1",
				KeyPath: "/keys/target",
				Jump: &dvp.sshEndpoint{
					User:    "jumper",
					Host:    "jump.example.com",
					KeyPath: "/keys/jump",
				},
			},
		},
		{
			name: "jump host inherits user and key from target",
			cfg: Config{
				SSHUser:     "deckhouse",
				SSHHost:     "10.0.0.1",
				SSHKeyPath:  "/keys/target",
				SSHJumpHost: "jump.example.com",
			},
			want: dvp.sshEndpoint{
				User:    "deckhouse",
				Host:    "10.0.0.1",
				KeyPath: "/keys/target",
				Jump: &dvp.sshEndpoint{
					User:    "deckhouse",
					Host:    "jump.example.com",
					KeyPath: "/keys/target",
				},
			},
		},
		{
			name: "jump host inherits only missing fields",
			cfg: Config{
				SSHUser:     "deckhouse",
				SSHHost:     "10.0.0.1",
				SSHKeyPath:  "/keys/target",
				SSHJumpHost: "jump.example.com",
				SSHJumpUser: "jumper",
			},
			want: dvp.sshEndpoint{
				User:    "deckhouse",
				Host:    "10.0.0.1",
				KeyPath: "/keys/target",
				Jump: &dvp.sshEndpoint{
					User:    "jumper",
					Host:    "jump.example.com",
					KeyPath: "/keys/target",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.baseEndpoint()

			if got.User != tt.want.User || got.Host != tt.want.Host || got.KeyPath != tt.want.KeyPath {
				t.Errorf("endpoint = {User:%q Host:%q KeyPath:%q}, want {User:%q Host:%q KeyPath:%q}",
					got.User, got.Host, got.KeyPath, tt.want.User, tt.want.Host, tt.want.KeyPath)
			}

			switch {
			case tt.want.Jump == nil && got.Jump != nil:
				t.Errorf("Jump = %+v, want nil", got.Jump)
			case tt.want.Jump != nil && got.Jump == nil:
				t.Fatalf("Jump = nil, want %+v", tt.want.Jump)
			case tt.want.Jump != nil && got.Jump != nil:
				if got.Jump.User != tt.want.Jump.User ||
					got.Jump.Host != tt.want.Jump.Host ||
					got.Jump.KeyPath != tt.want.Jump.KeyPath {
					t.Errorf("Jump = {User:%q Host:%q KeyPath:%q}, want {User:%q Host:%q KeyPath:%q}",
						got.Jump.User, got.Jump.Host, got.Jump.KeyPath,
						tt.want.Jump.User, tt.want.Jump.Host, tt.want.Jump.KeyPath)
				}
				if got.Jump.Jump != nil {
					t.Errorf("Jump.Jump = %+v, want nil (no nested jump chain)", got.Jump.Jump)
				}
			}
		})
	}
}
