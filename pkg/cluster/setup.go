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
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/deckhouse/storage-e2e/internal/config"
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

// PrepareBootstrapConfig prepares the bootstrap configuration file from a template.
// It takes cluster definition, master IP address, and VM IP addresses to calculate the internal network CIDR.
// The function generates a config file and saves it to the temp/ directory.
// Returns the path to the generated config file.
func PrepareBootstrapConfig(clusterDef *config.ClusterDefinition, masterIP string, vmIPs []string) (string, error) {
	if clusterDef == nil {
		return "", fmt.Errorf("clusterDef cannot be nil")
	}
	if masterIP == "" {
		return "", fmt.Errorf("masterIP cannot be empty")
	}
	if len(vmIPs) == 0 {
		return "", fmt.Errorf("vmIPs cannot be empty")
	}

	// Calculate internal network CIDR from VM IPs (assume /24 subnet)
	internalNetworkCIDR, err := calculateNetworkCIDR(vmIPs)
	if err != nil {
		return "", fmt.Errorf("failed to calculate network CIDR: %w", err)
	}

	// Format public domain template with master IP for sslip.io
	// Format: %s.10.10.1.5.sslip.io (dots in IP are preserved)
	publicDomainTemplate := fmt.Sprintf("%%s.%s.sslip.io", masterIP)

	// Prepare template data
	templateData := struct {
		PodSubnetCIDR        string
		ServiceSubnetCIDR    string
		KubernetesVersion    string
		ClusterDomain        string
		ImagesRepo           string
		RegistryDockerCfg    string
		PublicDomainTemplate string
		InternalNetworkCIDR  string
	}{
		PodSubnetCIDR:        clusterDef.DKPParameters.PodSubnetCIDR,
		ServiceSubnetCIDR:    clusterDef.DKPParameters.ServiceSubnetCIDR,
		KubernetesVersion:    clusterDef.DKPParameters.KubernetesVersion,
		ClusterDomain:        clusterDef.DKPParameters.ClusterDomain,
		ImagesRepo:           clusterDef.DKPParameters.RegistryRepo,
		RegistryDockerCfg:    config.RegistryDockerCfg,
		PublicDomainTemplate: publicDomainTemplate,
		InternalNetworkCIDR:  internalNetworkCIDR,
	}

	// Get the test file name from the caller
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return "", fmt.Errorf("failed to get caller file information")
	}
	testFileName := strings.TrimSuffix(filepath.Base(callerFile), filepath.Ext(callerFile))

	// Determine the temp directory path in the repo root
	// callerFile is in tests/{test-dir}/, so we go up two levels to reach repo root
	callerDir := filepath.Dir(callerFile)
	repoRootPath := filepath.Join(callerDir, "..", "..")
	// Resolve the .. parts to get absolute path
	repoRoot, err := filepath.Abs(repoRootPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve repo root path: %w", err)
	}

	// Template file path
	templatePath := filepath.Join(repoRoot, "files", "bootstrap", "config.yml.tpl")

	// Read template file
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read template file %s: %w", templatePath, err)
	}

	// Parse template
	tmpl, err := template.New("bootstrap-config").Parse(string(templateContent))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Determine temp directory path - same pattern as GetKubeconfig
	tempDir := filepath.Join(repoRoot, "temp", testFileName)

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory %s: %w", tempDir, err)
	}

	// Output file path
	outputPath := filepath.Join(tempDir, "config.yml")

	// Create output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file %s: %w", outputPath, err)
	}
	defer outputFile.Close()

	// Execute template and write to file
	if err := tmpl.Execute(outputFile, templateData); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return outputPath, nil
}

// calculateNetworkCIDR calculates the network CIDR that encompasses all VM IP addresses.
// It starts with a /24 network from the first IP and expands the network (reduces prefix length)
// until all IPs belong to the CIDR.
func calculateNetworkCIDR(vmIPs []string) (string, error) {
	if len(vmIPs) == 0 {
		return "", fmt.Errorf("vmIPs cannot be empty")
	}

	// Parse all IP addresses
	parsedIPs := make([]net.IP, 0, len(vmIPs))
	for _, ipStr := range vmIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return "", fmt.Errorf("invalid IP address: %s", ipStr)
		}
		// Convert to IPv4 if needed
		ipv4 := ip.To4()
		if ipv4 == nil {
			return "", fmt.Errorf("IP address is not IPv4: %s", ipStr)
		}
		parsedIPs = append(parsedIPs, ipv4)
	}

	// Start with /24 network from the first IP
	// Replace last octet with 0
	firstIP := make(net.IP, len(parsedIPs[0]))
	copy(firstIP, parsedIPs[0])
	firstIP[3] = 0 // Set last octet to 0

	// Start with /24 and expand until all IPs fit
	prefixLen := 24
	for prefixLen >= 16 {
		// Create network with current prefix length
		mask := net.CIDRMask(prefixLen, 32)
		network := firstIP.Mask(mask)
		cidrStr := fmt.Sprintf("%s/%d", network.String(), prefixLen)

		// Parse the CIDR to get network and mask
		_, ipNet, err := net.ParseCIDR(cidrStr)
		if err != nil {
			return "", fmt.Errorf("failed to parse CIDR %s: %w", cidrStr, err)
		}

		// Check if all IPs belong to this network
		allInNetwork := true
		for _, ip := range parsedIPs {
			if !ipNet.Contains(ip) {
				allInNetwork = false
				break
			}
		}

		if allInNetwork {
			return cidrStr, nil
		}

		// Expand network by reducing prefix length
		prefixLen--
	}

	return "", fmt.Errorf("failed to find a network CIDR that contains all IPs")
}

// UploadBootstrapFiles uploads the private key and config.yml file to the setup node.
// The private key is uploaded to /home/cloud/.ssh/id_rsa with permissions 0600.
// The config.yml file is uploaded to /home/cloud/config.yml.
func UploadBootstrapFiles(ctx context.Context, sshClient ssh.SSHClient, privateKeyPath, configPath string) error {
	if sshClient == nil {
		return fmt.Errorf("sshClient cannot be nil")
	}
	if privateKeyPath == "" {
		return fmt.Errorf("privateKeyPath cannot be empty")
	}
	if configPath == "" {
		return fmt.Errorf("configPath cannot be empty")
	}

	// Upload private key to /home/cloud/.ssh/id_rsa
	remoteKeyPath := "/home/cloud/.ssh/id_rsa"
	if err := sshClient.Upload(ctx, privateKeyPath, remoteKeyPath); err != nil {
		return fmt.Errorf("failed to upload private key to %s: %w", remoteKeyPath, err)
	}

	// Set permissions 0600 for the private key (no sudo needed, we own the file)
	cmd := "chmod 600 /home/cloud/.ssh/id_rsa"
	output, err := sshClient.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("failed to set permissions for private key: %w\nOutput: %s", err, output)
	}

	// Upload config.yml to /home/cloud/config.yml
	remoteConfigPath := "/home/cloud/config.yml"
	if err := sshClient.Upload(ctx, configPath, remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config.yml to %s: %w", remoteConfigPath, err)
	}

	return nil
}
