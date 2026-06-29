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

package vm

import (
	"strings"
	"testing"
)

func TestBuildCloudInit(t *testing.T) {
	tests := []struct {
		name       string
		withDocker bool
	}{
		{name: "cluster node", withDocker: false},
		{name: "setup node", withDocker: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := buildCloudInit(cloudInitOptions{
				hostname:         "node-1",
				sshAuthorizedKey: "ssh-ed25519 AAAA test",
				withDocker:       tt.withDocker,
			})

			mustContain := []string{
				"#cloud-config",
				"qemu-guest-agent",
				"systemctl enable --now qemu-guest-agent.service",
				"http://mirror.yandex.ru/ubuntu",
				`Acquire::ForceIPv4 "true";`,
				"AllowTcpForwarding yes",
				"ssh-ed25519 AAAA test",
				"hostnamectl set-hostname node-1",
				testStandUserPasswordHash,
			}
			for _, s := range mustContain {
				if !strings.Contains(out, s) {
					t.Errorf("cloud-init missing %q", s)
				}
			}

			hasDocker := strings.Contains(out, "docker.io")
			hasDockerService := strings.Contains(out, "systemctl enable --now docker.service")
			if hasDocker != tt.withDocker || hasDockerService != tt.withDocker {
				t.Errorf("withDocker=%v: docker.io=%v docker.service=%v", tt.withDocker, hasDocker, hasDockerService)
			}
		})
	}
}
