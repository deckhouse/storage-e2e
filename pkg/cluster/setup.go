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
	"encoding/base64"
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
	k8s "k8s.io/client-go/kubernetes"
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
// The function generates a config file and saves it to the temp/ directory.
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

	// Determine log file path: configPath is in temp/<test-name>/config.yml, so log goes to temp/<test-name>/bootstrap.log
	configDir := filepath.Dir(configPath)
	logFilePath := filepath.Join(configDir, "bootstrap.log")
	remoteLogPath := fmt.Sprintf("/tmp/bootstrap-%d.log", os.Getpid())    // Use unique name to avoid conflicts
	agentSocketPath := fmt.Sprintf("/tmp/ssh-agent-%d.sock", os.Getpid()) // Unique agent socket path

	// Step 2: Setup ssh-agent and add the SSH key
	// Create a temporary askpass script to provide the passphrase non-interactively
	askpassScriptPath := fmt.Sprintf("/tmp/ssh-askpass-%d.sh", os.Getpid())
	askpassScript := fmt.Sprintf(`#!/bin/bash
echo "%s"
`, config.SSHPassphrase)

	// Create the askpass script file on the remote host
	createAskpassCmd := fmt.Sprintf("sudo -u %s bash -c 'cat > %s << \"ASKPASS_EOF\"\n%sASKPASS_EOF\nchmod +x %s'", config.VMSSHUser, askpassScriptPath, askpassScript, askpassScriptPath)
	_, err = sshClient.Exec(ctx, createAskpassCmd)
	if err != nil {
		return fmt.Errorf("failed to create askpass script: %w", err)
	}

	// Setup ssh-agent and add the key
	setupAgentScript := fmt.Sprintf(`
		# Start ssh-agent with specified socket path
		eval $(ssh-agent -a %s) > /dev/null 2>&1
		export SSH_AUTH_SOCK=%s
		export SSH_AGENT_PID=$SSH_AGENT_PID
		
		# Add the SSH key to the agent using the askpass script
		if [ -n "%s" ]; then
			DISPLAY=:0 SSH_ASKPASS=%s ssh-add /home/%s/.ssh/id_rsa </dev/null 2>&1
		else
			ssh-add /home/%s/.ssh/id_rsa </dev/null 2>&1
		fi
		
		# Output the agent socket path for use in docker command
		echo $SSH_AUTH_SOCK
	`, agentSocketPath, agentSocketPath, config.SSHPassphrase, askpassScriptPath, config.VMSSHUser, config.VMSSHUser)

	// Run the agent setup script
	agentOutput, err := sshClient.Exec(ctx, fmt.Sprintf("sudo -u %s bash -c %s", config.VMSSHUser, fmt.Sprintf("'%s'", setupAgentScript)))
	if err != nil {
		// Clean up askpass script on error
		_, _ = sshClient.Exec(ctx, fmt.Sprintf("sudo rm -f %s", askpassScriptPath))
		return fmt.Errorf("failed to setup ssh-agent: %w\nOutput: %s", err, agentOutput)
	}

	// Extract the actual SSH_AUTH_SOCK path from output (last line)
	agentSocketLines := strings.Split(strings.TrimSpace(agentOutput), "\n")
	actualAgentSocket := agentSocketPath // Default to our specified path
	if len(agentSocketLines) > 0 {
		lastLine := strings.TrimSpace(agentSocketLines[len(agentSocketLines)-1])
		if lastLine != "" && strings.HasPrefix(lastLine, "/") {
			actualAgentSocket = lastLine
		}
	}

	// Make the socket readable by root (needed when docker runs with sudo)
	// This allows the docker process (running as root) to access the socket
	chmodCmd := fmt.Sprintf("sudo chmod 666 %s 2>/dev/null || true", actualAgentSocket)
	_, _ = sshClient.Exec(ctx, chmodCmd)

	// Step 3: Run dhctl bootstrap command with ssh-agent
	// Mount SSH_AUTH_SOCK into the container and use it for authentication
	// Note: We don't use --ssh-agent-private-keys anymore, dhctl will use SSH_AUTH_SOCK
	// Docker needs to run with sudo for access to docker socket
	installImage := fmt.Sprintf("%s/install:%s", registryRepo, devBranch)
	bootstrapCmd := fmt.Sprintf(
		"sudo -u %s bash -c 'export SSH_AUTH_SOCK=%s; sudo docker run --network=host --pull=always -v \"/home/%s/config.yml:/config.yml\" -v \"%s:/tmp/ssh-agent.sock\" -e SSH_AUTH_SOCK=/tmp/ssh-agent.sock %s dhctl bootstrap --ssh-host=%s --ssh-user=%s --config=/config.yml > %s 2>&1'",
		config.VMSSHUser, actualAgentSocket, config.VMSSHUser, actualAgentSocket, installImage, masterIP, config.VMSSHUser, remoteLogPath,
	)

	// Run the bootstrap command
	// Output is redirected to remote log file, so output variable will be empty
	output, err = sshClient.Exec(ctx, bootstrapCmd)

	// Clean up ssh-agent and askpass script after bootstrap (whether success or failure)
	cleanupAgentCmd := fmt.Sprintf("sudo -u %s bash -c 'SSH_AUTH_SOCK=%s ssh-agent -k 2>/dev/null || true; rm -f %s %s 2>/dev/null || true'", config.VMSSHUser, actualAgentSocket, actualAgentSocket, askpassScriptPath)
	_, _ = sshClient.Exec(ctx, cleanupAgentCmd)

	// Always download log file from remote host (whether success or failure)
	// Use sudo cat since the log file was created with sudo
	logContent, logErr := sshClient.Exec(ctx, fmt.Sprintf("sudo cat %s 2>/dev/null || echo ''", remoteLogPath))

	// Save log file locally
	if logErr == nil && logContent != "" {
		// Create local log file directory if it doesn't exist
		if mkdirErr := os.MkdirAll(configDir, 0755); mkdirErr == nil {
			// Write log content to local file
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

	clientset, err := k8s.NewForConfig(kubeconfig)
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

// GetSSHPrivateKeyPath returns the path to the SSH private key file.
// If SSHPrivateKey is a file path, it returns the expanded path.
// If SSHPrivateKey is a base64-encoded string, it decodes it, writes to a temporary file in temp/<test-name>/,
// and returns that path.
func GetSSHPrivateKeyPath() (string, error) {
	// Check if it looks like a file path (contains path separators or starts with ~)
	looksLikePath := strings.Contains(config.SSHPrivateKey, "/") || strings.HasPrefix(config.SSHPrivateKey, "~") || strings.Contains(config.SSHPrivateKey, "\\")

	if !looksLikePath {
		// Doesn't look like a path, try base64 decoding
		decoded, err := base64.StdEncoding.DecodeString(config.SSHPrivateKey)
		if err == nil && len(decoded) > 0 {
			// Successfully decoded, write to temp file in temp/<test-name>/
			// Get the test file name from the caller (same pattern as PrepareBootstrapConfig)
			_, callerFile, _, ok := runtime.Caller(1)
			if !ok {
				return "", fmt.Errorf("failed to get caller file information")
			}
			testFileName := strings.TrimSuffix(filepath.Base(callerFile), filepath.Ext(callerFile))

			// Determine the temp directory path in the repo root
			callerDir := filepath.Dir(callerFile)
			repoRootPath := filepath.Join(callerDir, "..", "..")
			repoRoot, err := filepath.Abs(repoRootPath)
			if err != nil {
				return "", fmt.Errorf("failed to resolve repo root path: %w", err)
			}

			// Create temp directory if it doesn't exist
			tempDir := filepath.Join(repoRoot, "temp", testFileName)
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				return "", fmt.Errorf("failed to create temp directory %s: %w", tempDir, err)
			}

			// Create temp file in temp/<test-name>/
			tmpFile, err := os.CreateTemp(tempDir, "ssh_private_key_*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file for private key: %w", err)
			}
			defer tmpFile.Close()

			if _, err := tmpFile.Write(decoded); err != nil {
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("failed to write decoded private key to temp file: %w", err)
			}

			// Set permissions to 0600
			if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("failed to set permissions on temp private key file: %w", err)
			}

			return tmpFile.Name(), nil
		}
		// If decoding failed, fall through to treat as path (might be a relative path without /)
	}

	// Treat as file path
	return expandPath(config.SSHPrivateKey)
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
