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
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
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

// WaitForSSHReady waits until port 22 is reachable on the target VM through
// the base cluster SSH client. This should be called after VMs reach "Running"
// state but before attempting a full SSH connection, because cloud-init may
// still be configuring networking and the SSH daemon.
func WaitForSSHReady(ctx context.Context, baseSSHClient ssh.SSHClient, targetIP string) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for SSH to be ready on %s", targetIP)
		case <-ticker.C:
			output, err := baseSSHClient.Exec(ctx, fmt.Sprintf("nc -z -w 5 %s 22 && echo SSH_READY", targetIP))
			if err == nil && strings.Contains(output, "SSH_READY") {
				return nil
			}
			logger.Debug("SSH not ready yet on %s, retrying...", targetIP)
		}
	}
}

// WaitForDockerReady waits for Docker to be ready on the setup node.
// Docker is installed via cloud-init during VM provisioning, so this function
// waits for cloud-init to complete and verifies Docker is working.
func WaitForDockerReady(ctx context.Context, sshClient ssh.SSHClient) error {
	// Wait for cloud-init to complete (Docker is installed via cloud-init packages)
	waitCmd := `
set -e

echo "Waiting for cloud-init to complete..."
sudo cloud-init status --wait || true

# Wait for Docker service to be available (cloud-init enables it)
max_wait=300
waited=0
echo "Waiting for Docker service to be ready..."
while [ $waited -lt $max_wait ]; do
    if sudo systemctl is-active --quiet docker 2>/dev/null; then
        echo "Docker service is active"
        break
    fi
    echo "Docker not ready yet, waiting... ($waited/$max_wait seconds)"
    sleep 10
    waited=$((waited + 10))
done

if [ $waited -ge $max_wait ]; then
    echo "Docker service did not become ready in time, attempting to start it..."
    sudo systemctl start docker || true
fi
`
	output, err := sshClient.Exec(ctx, waitCmd)
	if err != nil {
		return fmt.Errorf("failed to wait for cloud-init/Docker: %w\nOutput: %s", err, output)
	}

	// Verify Docker is working by running docker ps
	verifyCmd := "sudo docker ps"
	output, err = sshClient.Exec(ctx, verifyCmd)
	if err != nil {
		return fmt.Errorf("Docker is not working (docker ps failed): %w\nOutput: %s", err, output)
	}

	return nil
}

// PrepareBootstrapConfig prepares the bootstrap configuration file from a template.
// It takes cluster definition and extracts VM IP addresses to calculate the internal network CIDR.
// The function generates a config file and saves it to /tmp/e2e/.
// Returns the path to the generated config file.
// Note: clusterDef must have IPAddress fields filled in for all VM nodes (via GatherVMInfo)
func PrepareBootstrapConfig(clusterDef *config.ClusterDefinition) (string, error) {
	if clusterDef == nil {
		return "", fmt.Errorf("clusterDef cannot be nil")
	}

	// Extract VM IPs from cluster definition
	var vmIPs []string
	firstMasterIP := ""
	for _, master := range clusterDef.Masters {
		if master.HostType == config.HostTypeVM && master.IPAddress != "" {
			vmIPs = append(vmIPs, master.IPAddress)
			if firstMasterIP == "" {
				firstMasterIP = master.IPAddress
			}
		}
	}
	for _, worker := range clusterDef.Workers {
		if worker.HostType == config.HostTypeVM && worker.IPAddress != "" {
			vmIPs = append(vmIPs, worker.IPAddress)
		}
	}
	if clusterDef.Setup != nil && clusterDef.Setup.HostType == config.HostTypeVM && clusterDef.Setup.IPAddress != "" {
		vmIPs = append(vmIPs, clusterDef.Setup.IPAddress)
	}

	if len(vmIPs) == 0 {
		return "", fmt.Errorf("no VM IP addresses found in cluster definition (IPAddress fields must be filled via GatherVMInfo)")
	}
	if firstMasterIP == "" {
		return "", fmt.Errorf("no master IP address found in cluster definition")
	}

	// Calculate internal network CIDR from VM IPs (assume /24 subnet)
	internalNetworkCIDR, err := calculateNetworkCIDR(vmIPs)
	if err != nil {
		return "", fmt.Errorf("failed to calculate network CIDR: %w", err)
	}

	// Format public domain template with master IP for sslip.io
	// Format: %s.10.10.1.5.sslip.io (dots in IP are preserved)
	publicDomainTemplate := fmt.Sprintf("%%s.%s.sslip.io", firstMasterIP)

	// Default devBranch to "main" if not specified
	devBranch := clusterDef.DKPParameters.DevBranch
	if devBranch == "" {
		devBranch = "main"
	}

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
		DevBranch            string
	}{
		PodSubnetCIDR:        clusterDef.DKPParameters.PodSubnetCIDR,
		ServiceSubnetCIDR:    clusterDef.DKPParameters.ServiceSubnetCIDR,
		KubernetesVersion:    clusterDef.DKPParameters.KubernetesVersion,
		ClusterDomain:        clusterDef.DKPParameters.ClusterDomain,
		ImagesRepo:           clusterDef.DKPParameters.RegistryRepo,
		RegistryDockerCfg:    config.RegistryDockerCfg,
		PublicDomainTemplate: publicDomainTemplate,
		InternalNetworkCIDR:  internalNetworkCIDR,
		DevBranch:            devBranch,
	}

	// Resolve repo root from this source file's location to find the template
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to determine source file path")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		return "", fmt.Errorf("failed to resolve repo root path: %w", err)
	}

	templatePath := filepath.Join(repoRoot, "files", "bootstrap", "config.yml.tpl")
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to read template file %s: %w", templatePath, err)
	}

	tmpl, err := template.New("bootstrap-config").Parse(string(templateContent))
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	tempDir := config.E2ETempDir
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", tempDir, err)
	}

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

// getDevBranchFromConfig reads the devBranch value from the bootstrap config.yml file.
// It parses the YAML and extracts the devBranch from the InitConfiguration section.
func getDevBranchFromConfig(configPath string) (string, error) {
	if configPath == "" {
		return "", fmt.Errorf("configPath cannot be empty")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Parse YAML documents (the file contains multiple YAML documents separated by ---)
	documents := strings.Split(string(data), "---")
	for _, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		var initConfig struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Deckhouse  struct {
				DevBranch string `yaml:"devBranch"`
			} `yaml:"deckhouse"`
		}

		if err := yaml.Unmarshal([]byte(doc), &initConfig); err != nil {
			continue // Skip documents that don't match
		}

		// Check if this is an InitConfiguration
		if initConfig.Kind == "InitConfiguration" && initConfig.Deckhouse.DevBranch != "" {
			return initConfig.Deckhouse.DevBranch, nil
		}
	}

	return "", fmt.Errorf("devBranch not found in config file %s", configPath)
}

// dhctlSSHConfigManifest and dhctlSSHHostManifest match dhctl OpenAPI kinds under candi/openapi/dhctl (dhctl.deckhouse.io/v1).
type dhctlSSHConfigManifest struct {
	APIVersion          string                    `yaml:"apiVersion"`
	Kind                string                    `yaml:"kind"`
	SSHUser             string                    `yaml:"sshUser"`
	SSHPort             int32                     `yaml:"sshPort"`
	SSHAgentPrivateKeys []dhctlSSHAgentPrivateKey `yaml:"sshAgentPrivateKeys"`
}

type dhctlSSHAgentPrivateKey struct {
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase,omitempty"`
}

type dhctlSSHHostManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Host       string `yaml:"host"`
}

func buildDHCTLSSHConnectionConfig(pemKey, sshUser, masterHost, passphrase string) ([]byte, error) {
	cfg := dhctlSSHConfigManifest{
		APIVersion: "dhctl.deckhouse.io/v1",
		Kind:       "SSHConfig",
		SSHUser:    sshUser,
		SSHPort:    22,
		SSHAgentPrivateKeys: []dhctlSSHAgentPrivateKey{{
			Key:        strings.TrimSpace(pemKey) + "\n",
			Passphrase: passphrase,
		}},
	}
	hostDoc := dhctlSSHHostManifest{
		APIVersion: "dhctl.deckhouse.io/v1",
		Kind:       "SSHHost",
		Host:       masterHost,
	}
	cfgBytes, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal SSHConfig: %w", err)
	}
	hostBytes, err := yaml.Marshal(&hostDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal SSHHost: %w", err)
	}
	doc := "---\n" + strings.TrimSuffix(string(cfgBytes), "\n") + "\n---\n" + strings.TrimSuffix(string(hostBytes), "\n") + "\n"
	return []byte(doc), nil
}

// BootstrapCluster bootstraps a Kubernetes cluster from the setup node to the first master node.
// It performs the following steps:
// 1. Logs into the Docker registry using DKP_LICENSE_KEY from config
// 2. Runs the dhctl bootstrap command in a Docker container
// Note: clusterDef must have IPAddress fields filled in for all VM nodes (via GatherVMInfo)
func BootstrapCluster(ctx context.Context, sshClient ssh.SSHClient, clusterDef *config.ClusterDefinition, configPath string) error {
	if sshClient == nil {
		return fmt.Errorf("sshClient cannot be nil")
	}
	if clusterDef == nil {
		return fmt.Errorf("clusterDef cannot be nil")
	}
	if len(clusterDef.Masters) == 0 {
		return fmt.Errorf("cluster definition must have at least one master")
	}
	firstMaster := clusterDef.Masters[0]
	if firstMaster.IPAddress == "" {
		return fmt.Errorf("first master IP address is not set (must be filled via GatherVMInfo)")
	}
	masterIP := firstMaster.IPAddress
	if configPath == "" {
		return fmt.Errorf("configPath cannot be empty")
	}
	if config.VMSSHUser == "" {
		return fmt.Errorf("VMSSHUser cannot be empty in config")
	}
	if config.DKPLicenseKey == "" {
		return fmt.Errorf("DKPLicenseKey cannot be empty in config")
	}

	// Extract registry hostname from registry repo URL
	// Example: "dev-registry.deckhouse.io/sys/deckhouse-oss" -> "dev-registry.deckhouse.io"
	registryRepo := clusterDef.DKPParameters.RegistryRepo
	if registryRepo == "" {
		return fmt.Errorf("registryRepo cannot be empty in cluster definition")
	}
	registryHostname := strings.Split(registryRepo, "/")[0]
	if registryHostname == "" {
		return fmt.Errorf("failed to extract hostname from registry repo: %s", registryRepo)
	}

	// Read devBranch from config file
	// Example: "dev-registry.deckhouse.io/sys/deckhouse-oss" + "/install:" + "main" = "dev-registry.deckhouse.io/sys/deckhouse-oss/install:main"
	devBranch, err := getDevBranchFromConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to get devBranch from config: %w", err)
	}

	// Step 1: Login to Docker registry
	// Command: echo "$DKP_LICENSE_KEY" | docker login -u license-token --password-stdin $REGISTRY_HOSTNAME
	loginCmd := fmt.Sprintf("echo \"%s\" | sudo docker login -u license-token --password-stdin %s", config.DKPLicenseKey, registryHostname)
	output, err := sshClient.Exec(ctx, loginCmd)
	if err != nil {
		return fmt.Errorf("failed to login to Docker registry %s: %w\nOutput: %s", registryHostname, err, output)
	}

	logFilePath := filepath.Join(config.E2ETempDir, fmt.Sprintf("bootstrap-%s.log", time.Now().Format("2006-01-02_15-04-05")))
	remoteLogPath := fmt.Sprintf("/tmp/bootstrap-%d.log", os.Getpid()) // Use unique name to avoid conflicts

	// Bootstrap previously mounted SSH_AUTH_SOCK into the dhctl container so authentication went through ssh-agent.
	// After Deckhouse PR https://github.com/deckhouse/deckhouse/pull/19063, dhctl resolves SSH settings via lib-connection
	// ExtractConfig early in bootstrap; that path reads private key files from disk using paths derived from flags (default
	// ~/.ssh/id_rsa → /root/.ssh/id_rsa inside the install image where HOME is /root). Mounting only the agent socket is then
	// too late and fails with errors like "extract config: Failed to read private keys from flags". We bind-mount the same
	// key already placed on the VM by UploadBootstrapFiles and pass --ssh-agent-private-keys explicitly.
	//
	// When SSH_PASSPHRASE is set, dhctl cannot prompt inside the non-interactive container. dhctl also forbids combining
	// --connection-config with other SSH flags, so we upload a small dhctl connection manifest (SSHConfig + SSHHost) with inline
	// key PEM and passphrase; dhctl copies that into temp key files and fills PrivateKeysToPassPhrasesFromConfig (see dhctl
	// pkg/config/connection.go ParseConnectionConfigFromFile).
	const dhctlContainerSSHKeyPath = "/root/.ssh/id_rsa"
	remoteSSHPrivateKey := filepath.Join("/home", config.VMSSHUser, ".ssh", "id_rsa")

	var dockerVolFlags, dhctlSSHArgs string
	var remoteConnYAMLPath string // passphrase-only; removed ASAP after docker run (avoid long-lived secrets in /tmp)

	removeRemoteConnYAML := func() {
		if remoteConnYAMLPath == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = sshClient.Exec(cleanupCtx, fmt.Sprintf("sudo rm -f %s", remoteConnYAMLPath))
		remoteConnYAMLPath = ""
	}

	if config.SSHPassphrase != "" {
		if _, prepErr := sshClient.Exec(ctx, fmt.Sprintf(`sudo -u %s bash -lc 'mkdir -p "${HOME}/.config/storage-e2e" && chmod 700 "${HOME}/.config/storage-e2e"'`, config.VMSSHUser)); prepErr != nil {
			return fmt.Errorf("prepare setup-node dir for dhctl connection-config: %w", prepErr)
		}

		mktempOut, mktempErr := sshClient.Exec(ctx, fmt.Sprintf(`sudo -u %s bash -lc 'mktemp "${HOME}/.config/storage-e2e/dhctl-bootstrap-connection.XXXXXX.yaml"'`, config.VMSSHUser))
		if mktempErr != nil {
			return fmt.Errorf("create temp path for dhctl connection-config on setup node: %w", mktempErr)
		}
		remoteConnYAMLPath = strings.TrimSpace(strings.Split(strings.TrimSpace(mktempOut), "\n")[0])
		if remoteConnYAMLPath == "" {
			return fmt.Errorf("mktemp returned empty path for dhctl connection-config")
		}

		if _, probeErr := sshClient.Exec(ctx, fmt.Sprintf("sudo -u %s test -r %s", config.VMSSHUser, remoteSSHPrivateKey)); probeErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("SSH private key not readable at %s on setup node: %w", remoteSSHPrivateKey, probeErr)
		}

		pemOut, pemErr := sshClient.Exec(ctx, fmt.Sprintf("sudo -u %s cat %s", config.VMSSHUser, remoteSSHPrivateKey))
		if pemErr != nil {
			removeRemoteConnYAML()
			// Do not include pemOut in the error: Exec uses CombinedOutput and stdout may already contain key material.
			return fmt.Errorf("read bootstrap SSH private key from setup node for connection-config: %w", pemErr)
		}
		if strings.TrimSpace(pemOut) == "" {
			removeRemoteConnYAML()
			return fmt.Errorf("empty SSH private key at %s on setup node", remoteSSHPrivateKey)
		}

		connYAML, connErr := buildDHCTLSSHConnectionConfig(pemOut, config.VMSSHUser, masterIP, config.SSHPassphrase)
		if connErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("build dhctl connection-config: %w", connErr)
		}

		localConnFile, tmpErr := os.CreateTemp("", "dhctl-bootstrap-connection-*.yaml")
		if tmpErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("create temp connection-config: %w", tmpErr)
		}
		localConnPath := localConnFile.Name()
		defer func() { _ = os.Remove(localConnPath) }()

		if chmodErr := os.Chmod(localConnPath, 0600); chmodErr != nil {
			_ = localConnFile.Close()
			removeRemoteConnYAML()
			return fmt.Errorf("chmod temp connection-config: %w", chmodErr)
		}
		if _, writeErr := localConnFile.Write(connYAML); writeErr != nil {
			_ = localConnFile.Close()
			removeRemoteConnYAML()
			return fmt.Errorf("write temp connection-config: %w", writeErr)
		}
		if closeErr := localConnFile.Close(); closeErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("close temp connection-config: %w", closeErr)
		}

		if upErr := sshClient.Upload(ctx, localConnPath, remoteConnYAMLPath); upErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("upload dhctl connection-config to setup node: %w", upErr)
		}
		if _, chErr := sshClient.Exec(ctx, fmt.Sprintf("chmod 600 %s", remoteConnYAMLPath)); chErr != nil {
			removeRemoteConnYAML()
			return fmt.Errorf("chmod remote connection-config: %w", chErr)
		}

		dockerVolFlags = fmt.Sprintf(
			`-v "/home/%s/config.yml:/config.yml" -v "%s:/dhctl-connection.yaml:ro"`,
			config.VMSSHUser, remoteConnYAMLPath,
		)
		dhctlSSHArgs = "--connection-config=/dhctl-connection.yaml --config=/config.yml"
	} else {
		dockerVolFlags = fmt.Sprintf(
			`-v "/home/%s/config.yml:/config.yml" -v "%s:%s:ro"`,
			config.VMSSHUser, remoteSSHPrivateKey, dhctlContainerSSHKeyPath,
		)
		dhctlSSHArgs = fmt.Sprintf(
			"--ssh-host=%s --ssh-user=%s --ssh-agent-private-keys=%s --config=/config.yml",
			masterIP, config.VMSSHUser, dhctlContainerSSHKeyPath,
		)
	}

	// Step 2: Run dhctl bootstrap (Docker needs sudo for access to docker socket)
	installImage := fmt.Sprintf("%s/install:%s", registryRepo, devBranch)
	bootstrapCmd := fmt.Sprintf(
		`sudo -u %s bash -c 'sudo docker run --network=host --pull=always %s %s dhctl bootstrap %s > %s 2>&1'`,
		config.VMSSHUser,
		dockerVolFlags,
		installImage,
		dhctlSSHArgs,
		remoteLogPath,
	)

	// Run the bootstrap command
	// Output is redirected to remote log file, so output variable will be empty
	output, err = sshClient.Exec(ctx, bootstrapCmd)

	removeRemoteConnYAML()

	// Always download log file from remote host (whether success or failure)
	// Use sudo cat since the log file was created with sudo
	logContent, logErr := sshClient.Exec(ctx, fmt.Sprintf("sudo cat %s 2>/dev/null || echo ''", remoteLogPath))

	if logErr == nil && logContent != "" {
		if mkdirErr := os.MkdirAll(config.E2ETempDir, 0755); mkdirErr == nil {
			_ = os.WriteFile(logFilePath, []byte(logContent), 0644)
		}
	}

	// Clean up remote log file
	_, _ = sshClient.Exec(ctx, fmt.Sprintf("sudo rm -f %s", remoteLogPath))

	// If bootstrap failed, include log content in error
	if err != nil {
		baseErr := fmt.Errorf("failed to bootstrap cluster: %w", err)
		if logContent != "" {
			return fmt.Errorf("%w\n\nBootstrap log saved to: %s\n\nBootstrap log content:\n%s", baseErr, logFilePath, logContent)
		} else if output != "" {
			// Fallback to output if log file wasn't available
			return fmt.Errorf("%w\n\nOutput: %s", baseErr, output)
		}
		return baseErr
	}

	return nil
}

// AddNodesToCluster adds nodes to the cluster
// It performs the following steps:
// 1. Gets bootstrap scripts from secrets
// 2. Runs bootstrap scripts on each node via SSH
// Note: NodeGroup must be created before calling this function (secrets won't appear until NodeGroup exists)
// Note: clusterDef must have IPAddress fields filled in for all VM nodes (via GatherVMInfo)
func AddNodesToCluster(ctx context.Context, kubeconfig *rest.Config, clusterDef *config.ClusterDefinition, baseSSHUser, baseSSHHost, sshKeyPath string) error {
	if kubeconfig == nil {
		return fmt.Errorf("kubeconfig cannot be nil")
	}
	if clusterDef == nil {
		return fmt.Errorf("clusterDef cannot be nil")
	}

	// Step 1: Get bootstrap scripts from secrets
	workerBootstrapScript, err := kubernetes.GetSecretDataValue(ctx, kubeconfig, "d8-cloud-instance-manager", "manual-bootstrap-for-worker", "bootstrap.sh")
	if err != nil {
		return fmt.Errorf("failed to get worker bootstrap script: %w", err)
	}

	masterBootstrapScript, err := kubernetes.GetSecretDataValue(ctx, kubeconfig, "d8-cloud-instance-manager", "manual-bootstrap-for-master", "bootstrap.sh")
	if err != nil {
		return fmt.Errorf("failed to get master bootstrap script: %w", err)
	}

	// Process additional masters and all workers in parallel
	masterCount := len(clusterDef.Masters) - 1
	workerCount := len(clusterDef.Workers)
	totalNodes := masterCount + workerCount

	if totalNodes == 0 {
		return nil
	}

	logger.Debug("Adding %d node(s) to the cluster in parallel (%d master(s), %d worker(s))", totalNodes, masterCount, workerCount)

	var wg sync.WaitGroup
	var mu sync.Mutex // for thread-safe printing
	errChan := make(chan error, totalNodes)

	// Add additional masters in parallel (skip the first one)
	if masterCount > 0 {
		for i := 1; i < len(clusterDef.Masters); i++ {
			wg.Add(1)
			go func(node config.ClusterNode) {
				defer wg.Done()
				if err := addNodeToCluster(ctx, node, masterBootstrapScript, clusterDef, baseSSHUser, baseSSHHost, sshKeyPath, true, &mu); err != nil {
					errChan <- fmt.Errorf("failed to add master node %s: %w", node.Hostname, err)
				}
			}(clusterDef.Masters[i])
		}
	}

	// Add all workers in parallel
	if workerCount > 0 {
		for _, workerNode := range clusterDef.Workers {
			wg.Add(1)
			go func(node config.ClusterNode) {
				defer wg.Done()
				if err := addNodeToCluster(ctx, node, workerBootstrapScript, clusterDef, baseSSHUser, baseSSHHost, sshKeyPath, false, &mu); err != nil {
					errChan <- fmt.Errorf("failed to add worker node %s: %w", node.Hostname, err)
				}
			}(workerNode)
		}
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Check if any errors occurred
	if len(errChan) > 0 {
		return <-errChan
	}

	return nil
}

// addNodeToCluster adds a single node to the cluster by running the bootstrap script
// mu parameter is optional - pass nil for sequential execution, or a mutex for thread-safe printing in parallel execution
// isMaster indicates whether the node is a master node (true) or worker node (false)
func addNodeToCluster(ctx context.Context, node config.ClusterNode, bootstrapScript string, clusterDef *config.ClusterDefinition, baseSSHUser, baseSSHHost, sshKeyPath string, isMaster bool, mu *sync.Mutex) error {
	// Get node IP address from cluster definition
	nodeIP, err := GetNodeIPAddress(clusterDef, node.Hostname)
	if err != nil {
		return fmt.Errorf("failed to get IP address for node %s: %w", node.Hostname, err)
	}

	// Log start of node addition
	nodeType := "worker"
	if isMaster {
		nodeType = "master"
	}

	if mu != nil {
		mu.Lock()
	}
	logger.Debug("Adding %s node %s (%s) to the cluster...", nodeType, node.Hostname, nodeIP)
	if mu != nil {
		mu.Unlock()
	}

	// Create SSH client to the node through jump host (base cluster master)
	sshClient, err := ssh.NewClientWithJumpHost(
		baseSSHUser, baseSSHHost, sshKeyPath, // jump host
		config.VMSSHUser, nodeIP, sshKeyPath, // target host
	)
	if err != nil {
		if mu != nil {
			mu.Lock()
		}
		logger.Error("Failed to create SSH connection to node %s (%s): %v", node.Hostname, nodeIP, err)
		if mu != nil {
			mu.Unlock()
		}
		return fmt.Errorf("failed to create SSH client to node %s (%s): %w", node.Hostname, nodeIP, err)
	}
	defer sshClient.Close()

	// Log that bootstrap script is starting
	if mu != nil {
		mu.Lock()
	}
	logger.Progress("Running bootstrap script on node %s (%s)...", node.Hostname, nodeIP)
	if mu != nil {
		mu.Unlock()
	}

	// Run bootstrap script as root with exponential retry
	// Note: The bootstrap script from secret is already decoded (Kubernetes API returns decoded data)
	// Retry logic handles temporary failures like proxy pod restarts (HTTP 401, connection errors, etc.)
	cmd := fmt.Sprintf("sudo bash << 'BOOTSTRAP_EOF'\n%s\nBOOTSTRAP_EOF", bootstrapScript)

	maxRetries := config.SSHRetryCount
	var output string
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		output, lastErr = sshClient.Exec(ctx, cmd)

		if lastErr == nil {
			// Success - log and return
			if mu != nil {
				mu.Lock()
			}
			if attempt > 1 {
				logger.Success("Bootstrap script completed successfully on node %s (%s) after %d attempts", node.Hostname, nodeIP, attempt)
			} else {
				logger.Success("Bootstrap script completed successfully on node %s (%s)", node.Hostname, nodeIP)
			}
			if mu != nil {
				mu.Unlock()
			}
			return nil
		}

		// Check if error is retryable (401 Unauthorized, connection errors, etc.)
		isRetryable := strings.Contains(output, "HTTP Error 401") ||
			strings.Contains(output, "Unauthorized") ||
			strings.Contains(output, "Connection refused") ||
			strings.Contains(output, "proxy endpoints failed")

		if !isRetryable || attempt == maxRetries {
			// Non-retryable error or max retries reached
			if mu != nil {
				mu.Lock()
			}
			if attempt > 1 {
				logger.Error("Bootstrap script failed on node %s (%s) after %d attempts: %v", node.Hostname, nodeIP, attempt, lastErr)
			} else {
				logger.Error("Bootstrap script failed on node %s (%s): %v", node.Hostname, nodeIP, lastErr)
			}
			if output != "" {
				logger.Debug("Bootstrap script output from node %s:\n%s", node.Hostname, output)
			}
			if mu != nil {
				mu.Unlock()
			}
			return fmt.Errorf("failed to run bootstrap script on node %s after %d attempts: %w\nOutput: %s", node.Hostname, attempt, lastErr, output)
		}

		// Retryable error - wait with exponential backoff
		backoffDuration := config.SSHRetryInitialDelay * time.Duration(1<<uint(attempt-1))
		if backoffDuration > config.SSHRetryMaxDelay {
			backoffDuration = config.SSHRetryMaxDelay
		}
		if mu != nil {
			mu.Lock()
		}
		logger.Warn("Bootstrap script failed on node %s (%s) (attempt %d/%d), retrying in %v: %v",
			node.Hostname, nodeIP, attempt, maxRetries, backoffDuration, lastErr)
		if mu != nil {
			mu.Unlock()
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while retrying bootstrap script on node %s: %w", node.Hostname, ctx.Err())
		case <-time.After(backoffDuration):
			// Continue to next retry
		}
	}

	// Should never reach here, but just in case
	return fmt.Errorf("failed to run bootstrap script on node %s after %d attempts: %w", node.Hostname, maxRetries, lastErr)
}

// WaitForAllNodesReady waits for all expected nodes to become Ready in parallel
// It validates that:
// 1. All expected nodes are present in the cluster
// 2. All nodes are in Ready state
// Expected nodes: all masters (including the first one that was bootstrapped) + all workers
func WaitForAllNodesReady(ctx context.Context, kubeconfig *rest.Config, clusterDef *config.ClusterDefinition, timeout time.Duration) error {
	if kubeconfig == nil {
		return fmt.Errorf("kubeconfig cannot be nil")
	}
	if clusterDef == nil {
		return fmt.Errorf("clusterDef cannot be nil")
	}

	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Build expected node names list
	expectedNodeNames := make([]string, 0)
	for _, master := range clusterDef.Masters {
		expectedNodeNames = append(expectedNodeNames, master.Hostname)
	}
	for _, worker := range clusterDef.Workers {
		expectedNodeNames = append(expectedNodeNames, worker.Hostname)
	}

	expectedCount := len(expectedNodeNames)
	if expectedCount == 0 {
		return fmt.Errorf("no nodes expected in cluster definition")
	}

	// Create context with timeout for all goroutines
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex // for thread-safe printing
	errChan := make(chan error, expectedCount)

	// Wait for each node to become ready in parallel
	for i, nodeName := range expectedNodeNames {
		wg.Add(1)
		go func(index int, name string) {
			defer wg.Done()

			mu.Lock()
			logger.Progress("Waiting for node %d/%d: %s", index+1, expectedCount, name)
			mu.Unlock()

			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-waitCtx.Done():
					errChan <- fmt.Errorf("timeout waiting for node %s to become Ready", name)
					return
				case <-ticker.C:
					node, err := clientset.CoreV1().Nodes().Get(waitCtx, name, metav1.GetOptions{})
					if err != nil {
						// Node doesn't exist yet, continue waiting
						continue
					}

					// Check if node is ready
					isReady := false
					for _, condition := range node.Status.Conditions {
						if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
							isReady = true
							break
						}
					}

					if isReady {
						mu.Lock()
						logger.Success("Node %s is Ready", name)
						mu.Unlock()
						return
					}
				}
			}
		}(i, nodeName)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Check if any errors occurred
	if len(errChan) > 0 {
		return <-errChan
	}

	return nil
}

// GetSSHPublicKeyContent returns the SSH public key content as a string.
// If SSHPublicKey is a file path, it reads and returns the file content.
// If SSHPublicKey is a plain-text string, it returns it directly.
func GetSSHPublicKeyContent() (string, error) {
	if config.SSHPublicKey == "" {
		return "", fmt.Errorf("SSH_PUBLIC_KEY is not set")
	}

	// Check if it looks like a file path (contains / or ~)
	if strings.Contains(config.SSHPublicKey, "/") || strings.HasPrefix(config.SSHPublicKey, "~") {
		// Treat as file path
		expandedPath, err := expandPath(config.SSHPublicKey)
		if err != nil {
			return "", fmt.Errorf("failed to expand public key path: %w", err)
		}

		content, err := os.ReadFile(expandedPath)
		if err != nil {
			return "", fmt.Errorf("failed to read public key file %s: %w", expandedPath, err)
		}

		// Trim whitespace (public key files often have trailing newlines)
		return strings.TrimSpace(string(content)), nil
	}

	// Treat as plain-text public key
	return strings.TrimSpace(config.SSHPublicKey), nil
}

// expandPath expands ~ to home directory
func expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	if path == "~" {
		return usr.HomeDir, nil
	}

	return filepath.Join(usr.HomeDir, strings.TrimPrefix(path, "~/")), nil
}
