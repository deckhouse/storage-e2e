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

// testStandUserPasswordHash is the SHA-512 crypt hash of the password "cloud"
// for the default "cloud" user. These VMs are short-lived, throwaway e2e test
// fixtures on an isolated base cluster; the well-known password only enables
// console login for debugging and never guards anything sensitive. It is kept
// as a named constant (rather than a bare literal) to document that intent.
const testStandUserPasswordHash = `$6$rounds=4096$vln/.aPHBOI7BMYR$bBMkqQvuGs5Gyd/1H5DP4m9HjQSy.kgrxpaGEHwkX7KEFV8BS.HZWPitAtZ2Vd8ZqIZRqmlykRCagTgPejt1i.`

// cloudInitAptMirror points apt at mirror.yandex.ru for both the primary
// archive and security pools. The default Ubuntu mirrors round-robin across
// many IPs that are partially unreachable from some Flant infra (IPv6 endpoints
// blocked, most IPv4 archive.ubuntu.com addresses time out), which makes
// package installation flaky or stalls it outright. mirror.yandex.ru carries
// the same suites and is reachable in those environments.
const cloudInitAptMirror = `apt:
  primary:
    - arches: [default]
      uri: http://mirror.yandex.ru/ubuntu
  security:
    - arches: [default]
      uri: http://mirror.yandex.ru/ubuntu
`

// cloudInitForceIPv4Apt disables IPv6 for apt so package fetches do not incur a
// 30-second connection timeout on hosts that lack working IPv6 egress. It is
// written before package_update runs.
const cloudInitForceIPv4Apt = `  - path: /etc/apt/apt.conf.d/99force-ipv4
    content: |
      Acquire::ForceIPv4 "true";
`

// cloudInitOptions controls the optional pieces of the generated cloud-init.
type cloudInitOptions struct {
	// hostname is set on the VM via hostnamectl.
	hostname string
	// sshAuthorizedKey is added to the "cloud" user's authorized_keys.
	sshAuthorizedKey string
	// withDocker installs and enables docker.io. It is used for the setup
	// (bootstrap) node, which runs the Deckhouse installer.
	withDocker bool
}

// basePackages are installed on every node. qemu-guest-agent is mandatory: the
// hypervisor reports a VM's IP address only once the guest agent is running, so
// without it the provisioner can never gather the VM IP.
var basePackages = []string{
	"tmux",
	"htop",
	"qemu-guest-agent",
	"iputils-ping",
	"jq",
	"curl",
}

// buildCloudInit renders a single cloud-config document. It is deterministic and
// pure: the same options always yield the same output. The withDocker option is
// the only difference between a cluster node and the setup node, so both share
// this one template instead of two near-identical functions.
func buildCloudInit(opts cloudInitOptions) string {
	packages := append([]string{}, basePackages...)
	if opts.withDocker {
		packages = append(packages, "docker.io")
	}

	var pkgList strings.Builder
	for _, p := range packages {
		fmt.Fprintf(&pkgList, "  - %s\n", p)
	}

	runcmd := []string{
		"systemctl restart ssh",
		fmt.Sprintf("hostnamectl set-hostname %s", opts.hostname),
		"systemctl daemon-reload",
		"systemctl enable --now qemu-guest-agent.service",
	}
	if opts.withDocker {
		runcmd = append(runcmd, "systemctl enable --now docker.service")
	}

	var runcmdList strings.Builder
	for _, c := range runcmd {
		fmt.Fprintf(&runcmdList, "  - %s\n", c)
	}

	return fmt.Sprintf(`#cloud-config
%spackage_update: true
packages:
%s
ssh_pwauth: true
users:
  - name: cloud
    passwd: %s
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    chpasswd: {expire: False}
    lock_passwd: false
    ssh_authorized_keys:
      - %s
write_files:
%s  - path: /etc/ssh/sshd_config.d/allow_tcp_forwarding.conf
    content: |
      AllowTcpForwarding yes
runcmd:
%s`,
		cloudInitAptMirror,
		strings.TrimRight(pkgList.String(), "\n"),
		testStandUserPasswordHash,
		opts.sshAuthorizedKey,
		cloudInitForceIPv4Apt,
		runcmdList.String(),
	)
}
