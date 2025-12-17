/*
Copyright 2025 Flant JSC

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

package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

// OSInfo represents detected operating system information
type OSInfo struct {
	ID            string // OS ID (e.g., "debian", "ubuntu", "centos", "redos", "astra", "altlinux")
	IDLike        string // OS ID_LIKE (e.g., "debian", "rhel fedora")
	VersionID     string // OS version ID
	PrettyName    string // OS pretty name
	KernelVersion string // Kernel version (e.g., "5.15.0-91-generic")
}

// GetOSInfo detects the operating system and kernel version on a remote host via SSH.
// This function reads /etc/os-release and runs uname -r to gather OS information.
func GetOSInfo(ctx context.Context, sshClient ssh.SSHClient) (*OSInfo, error) {
	// Read /etc/os-release file
	output, err := sshClient.Exec(ctx, "cat /etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("failed to read /etc/os-release: %w", err)
	}

	osInfo := &OSInfo{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")

		switch key {
		case "ID":
			osInfo.ID = strings.ToLower(value)
		case "ID_LIKE":
			osInfo.IDLike = strings.ToLower(value)
		case "VERSION_ID":
			osInfo.VersionID = value
		case "PRETTY_NAME":
			osInfo.PrettyName = value
		}
	}

	if osInfo.ID == "" {
		return nil, fmt.Errorf("failed to detect OS ID from /etc/os-release")
	}

	// Detect kernel version
	kernelOutput, err := sshClient.Exec(ctx, "uname -r")
	if err != nil {
		return nil, fmt.Errorf("failed to detect kernel version: %w", err)
	}
	osInfo.KernelVersion = strings.TrimSpace(kernelOutput)

	return osInfo, nil
}

// InstallDocker installs Docker on the remote host via SSH.
// Since the setup node is always Ubuntu 22.04, this function uses apt to install docker.io.
// It runs: apt update && apt install docker.io -y, then starts docker and verifies with docker ps.
func InstallDocker(ctx context.Context, sshClient ssh.SSHClient) error {
	// Update package list and install docker.io
	cmd := "sudo apt-get update && sudo apt-get install -y docker.io"
	output, err := sshClient.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to update packages and install docker.io: %w\nOutput: %s", err, output)
	}

	// Start Docker service
	cmd = "sudo systemctl start docker"
	output, err = sshClient.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to start docker service: %w\nOutput: %s", err, output)
	}

	// Verify Docker is working by running docker ps
	cmd = "sudo docker ps"
	output, err = sshClient.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to verify Docker installation (docker ps failed): %w\nOutput: %s", err, output)
	}

	return nil
}
