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
	"fmt"
	"strings"
)

const cloudInitAptMirror = `apt:
  primary:
    - arches: [default]
      uri: http://mirror.yandex.ru/ubuntu
  security:
    - arches: [default]
      uri: http://mirror.yandex.ru/ubuntu
`

const cloudInitForceIPv4Apt = `  - path: /etc/apt/apt.conf.d/99force-ipv4
    content: |
      Acquire::ForceIPv4 "true";
`

// cloudInitKubectlAliases is written to cluster nodes so operators get the
// familiar `k` alias + completion when debugging storage on the node.
const cloudInitKubectlAliases = `  - path: /root/.kubectl_aliases
    content: |
      alias k=kubectl
      complete -o default -F __start_kubectl k
`

type cloudInitOptions struct {
	hostname         string
	sshAuthorizedKey string
	withStorageTools bool
	withDocker       bool
}

var basePackages = []string{
	"tmux",
	"htop",
	"qemu-guest-agent",
	"iputils-ping",
	"jq",
	"curl",
}

// storageToolPackages are the tools the storage e2e suites rely on for load
// generation and data integrity checks on cluster nodes.
var storageToolPackages = []string{
	"stress-ng",
	"yq",
	"rsync",
	"fio",
}

func buildCloudInit(opts cloudInitOptions) string {
	packages := append([]string{}, basePackages...)
	if opts.withStorageTools {
		packages = append(packages, storageToolPackages...)
	}
	if opts.withDocker {
		packages = append(packages, "docker.io")
	}

	var pkgList strings.Builder
	for _, p := range packages {
		fmt.Fprintf(&pkgList, "  - %s\n", p)
	}

	runcmd := []string{
		"systemctl restart ssh",
		fmt.Sprintf("hostnamectl set-hostname '%s'", opts.hostname),
		"systemctl daemon-reload",
		"systemctl enable --now qemu-guest-agent.service",
	}
	if opts.withStorageTools {
		runcmd = append(runcmd, "echo 'source /root/.kubectl_aliases' >> /root/.bashrc")
	}
	if opts.withDocker {
		runcmd = append(runcmd, "systemctl enable --now docker.service")
	}

	var runcmdList strings.Builder
	for _, c := range runcmd {
		fmt.Fprintf(&runcmdList, "  - %s\n", c)
	}

	writeFiles := cloudInitForceIPv4Apt
	if opts.withStorageTools {
		writeFiles += cloudInitKubectlAliases
	}

	return fmt.Sprintf(`#cloud-config
%spackage_update: true
packages:
%s
ssh_pwauth: false
users:
  - name: cloud
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - "%s"
write_files:
%s  - path: /etc/ssh/sshd_config.d/allow_tcp_forwarding.conf
    content: |
      AllowTcpForwarding yes
runcmd:
%s`,
		cloudInitAptMirror,
		strings.TrimRight(pkgList.String(), "\n"),
		opts.sshAuthorizedKey,
		writeFiles,
		runcmdList.String(),
	)
}
