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
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

// LoadClusterConfig loads and validates a cluster configuration from a YAML file
// The config file is expected to be in the same directory as the caller (typically the test file)
func LoadClusterConfig(configFilename string) (*config.ClusterDefinition, error) {
	// Get the caller's file path (the test file that called this function)
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return nil, fmt.Errorf("failed to determine caller file path")
	}
	callerDir := filepath.Dir(callerFile)
	yamlConfigPath := filepath.Join(callerDir, configFilename)

	// Read the YAML file
	data, err := os.ReadFile(yamlConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", yamlConfigPath, err)
	}

	// Parse YAML directly into ClusterDefinition (has custom UnmarshalYAML for root key)
	var clusterDef config.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Validate the configuration
	if err := validateClusterConfig(&clusterDef); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &clusterDef, nil
}

// validateClusterConfig validates the cluster configuration
func validateClusterConfig(cfg *config.ClusterDefinition) error {
	// Validate that at least one master exists
	if len(cfg.Masters) == 0 {
		return fmt.Errorf("at least one master node is required")
	}

	// Validate master nodes
	for i, master := range cfg.Masters {
		if err := validateNode(master, true); err != nil {
			return fmt.Errorf("master[%d] validation failed: %w", i, err)
		}
	}

	// Validate worker nodes
	for i, worker := range cfg.Workers {
		if err := validateNode(worker, false); err != nil {
			return fmt.Errorf("worker[%d] validation failed: %w", i, err)
		}
	}

	// Validate setup node if present
	if cfg.Setup != nil {
		if err := validateNode(*cfg.Setup, false); err != nil {
			return fmt.Errorf("setup node validation failed: %w", err)
		}
	}

	// Validate DKP parameters
	dkpParams := cfg.DKPParameters
	if dkpParams.PodSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.podSubnetCIDR is required")
	}
	if dkpParams.ServiceSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.serviceSubnetCIDR is required")
	}
	if dkpParams.ClusterDomain == "" {
		return fmt.Errorf("dkpParameters.clusterDomain is required")
	}
	if dkpParams.RegistryRepo == "" {
		return fmt.Errorf("dkpParameters.registryRepo is required")
	}

	return nil
}

// validateNode validates a single node configuration
func validateNode(node config.ClusterNode, isMaster bool) error {
	if node.Hostname == "" {
		return fmt.Errorf("hostname is required")
	}

	if node.HostType == config.HostTypeVM {
		if node.CPU <= 0 {
			return fmt.Errorf("CPU must be greater than 0 for VM nodes")
		}
		if node.RAM <= 0 {
			return fmt.Errorf("RAM must be greater than 0 for VM nodes")
		}
		if node.DiskSize <= 0 {
			return fmt.Errorf("diskSize must be greater than 0 for VM nodes")
		}
	}

	if node.Auth.User == "" {
		return fmt.Errorf("auth.user is required")
	}

	if node.Auth.Method == config.AuthMethodSSHKey && node.Auth.SSHKey == "" {
		return fmt.Errorf("auth.sshKey is required when using ssh-key authentication")
	}

	if node.Auth.Method == config.AuthMethodSSHPass && node.Auth.Password == "" {
		return fmt.Errorf("auth.password is required when using ssh-password authentication")
	}

	return nil
}

// expandPath expands ~ to home directory and resolves symlinks if present
func expandPath(path string) (string, error) {
	var expandedPath string

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}

		if path == "~" {
			expandedPath = homeDir
		} else {
			expandedPath = filepath.Join(homeDir, strings.TrimPrefix(path, "~/"))
		}
	} else {
		expandedPath = path
	}

	// Resolve symlinks if present (usually it won't be a symlink)
	// If resolution fails (e.g., path doesn't exist or is not a symlink), use the expanded path
	resolvedPath, err := filepath.EvalSymlinks(expandedPath)
	if err != nil {
		// Path might not exist yet or might not be a symlink - use expanded path as-is
		return expandedPath, nil
	}

	return resolvedPath, nil
}

// GetKubeconfig connects to the master node via SSH, retrieves kubeconfig from /etc/kubernetes/admin.conf,
// and returns a rest.Config that can be used with Kubernetes clients, along with the path to the kubeconfig file.
// If sshClient is provided, it will be used instead of creating a new connection.
// If sshClient is nil, a new connection will be created and closed automatically.
func GetKubeconfig(masterIP, user, keyPath string, sshClient ssh.SSHClient) (*rest.Config, string, error) {
	// Create SSH client if not provided
	shouldClose := false
	if sshClient == nil {
		var err error
		sshClient, err = ssh.NewClient(user, masterIP, keyPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create SSH client: %w", err)
		}
		shouldClose = true
	}
	if shouldClose {
		defer sshClient.Close()
	}

	// Get the test file name from the caller
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return nil, "", fmt.Errorf("failed to get caller file information")
	}
	testFileName := strings.TrimSuffix(filepath.Base(callerFile), filepath.Ext(callerFile))

	// Determine the temp directory path in the repo root
	// callerFile is in tests/{test-dir}/, so we go up two levels to reach repo root
	callerDir := filepath.Dir(callerFile)
	repoRootPath := filepath.Join(callerDir, "..", "..")
	// Resolve the .. parts to get absolute path
	repoRoot, err := filepath.Abs(repoRootPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve repo root path: %w", err)
	}
	tempDir := filepath.Join(repoRoot, "temp", testFileName)

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create temp directory %s: %w", tempDir, err)
	}

	// Create kubeconfig file path in temp directory
	kubeconfigPath := filepath.Join(tempDir, fmt.Sprintf("kubeconfig-%s.yml", masterIP))

	var kubeconfigContent []byte

	// Try to read kubeconfig from /etc/kubernetes/admin.conf via SSH
	ctx := context.Background()
	kubeconfigContentStr, err := sshClient.Exec(ctx, "sudo -n cat /etc/kubernetes/admin.conf")
	if err != nil {
		// SSH retrieval failed (likely due to sudo password requirement)
		// Try to use KUBE_CONFIG_PATH if set, otherwise notify user
		if config.KubeConfigPath != "" {
			// Expand path to handle ~ and resolve symlinks if present
			resolvedPath, err := expandPath(config.KubeConfigPath)
			if err != nil {
				return nil, "", fmt.Errorf("failed to expand KUBE_CONFIG_PATH (%s): %w", config.KubeConfigPath, err)
			}
			// Read kubeconfig content from the provided file
			kubeconfigContent, err = os.ReadFile(resolvedPath)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read kubeconfig from KUBE_CONFIG_PATH (%s): %w", resolvedPath, err)
			}
		} else {
			// KUBE_CONFIG_PATH not set, notify user and fail
			return nil, "", fmt.Errorf("failed to read kubeconfig from master (this may occur if sudo requires a password). "+
				"Please download the kubeconfig file manually and provide its full path via KUBE_CONFIG_PATH environment variable. "+
				"Original error: %w", err)
		}
	} else {
		// SSH succeeded - use the content from SSH
		kubeconfigContent = []byte(kubeconfigContentStr)
	}

	// Write kubeconfig content to temp file (always copy to temp, regardless of source)
	kubeconfigFile, err := os.Create(kubeconfigPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create kubeconfig file %s: %w", kubeconfigPath, err)
	}

	if _, err := kubeconfigFile.Write(kubeconfigContent); err != nil {
		kubeconfigFile.Close()
		return nil, "", fmt.Errorf("failed to write kubeconfig to file: %w", err)
	}
	if err := kubeconfigFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close kubeconfig file: %w", err)
	}

	// Build rest.Config from the kubeconfig file in temp directory
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, kubeconfigPath, nil
}

// UpdateKubeconfigPort updates the kubeconfig file to use the specified local port
// It replaces the server URL with 127.0.0.1:port
func UpdateKubeconfigPort(kubeconfigPath string, localPort int) error {
	content, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig file: %w", err)
	}

	contentStr := string(content)
	// Replace server URL with localhost and new port
	// Common patterns: server: https://<ip>:6445 or server: https://127.0.0.1:6445
	// Also handle:    server: https://<ip>:6443 (standard k8s port)
	lines := strings.Split(contentStr, "\n")
	updated := false
	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "server:") {
			// Replace the entire server URL with 127.0.0.1:port
			// Pattern: server: https://<host>:<port>
			if strings.Contains(trimmedLine, "https://") {
				// Find the URL part and replace it
				urlStart := strings.Index(trimmedLine, "https://")
				if urlStart != -1 {
					// Replace the URL with localhost:port
					// Preserve any indentation before "server:"
					indent := ""
					for j := 0; j < len(line) && (line[j] == ' ' || line[j] == '\t'); j++ {
						indent += string(line[j])
					}
					newURL := fmt.Sprintf("https://127.0.0.1:%d", localPort)
					lines[i] = indent + "server: " + newURL
					updated = true
				}
			}
		}
	}

	if !updated {
		return fmt.Errorf("could not find server URL in kubeconfig to update")
	}

	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(kubeconfigPath, []byte(newContent), 0600); err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	return nil
}
