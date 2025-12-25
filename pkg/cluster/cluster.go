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
	"path/filepath"
	"runtime"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"os"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/apps"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/core"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"gopkg.in/yaml.v3"
)

// TestClusterResources holds all resources created for a test cluster connection
type TestClusterResources struct {
	SSHClient          ssh.SSHClient
	Kubeconfig         *rest.Config
	KubeconfigPath     string
	TunnelInfo         *ssh.TunnelInfo
	ClusterDefinition  *config.ClusterDefinition
	VMResources        *VMResources
	BaseClusterClient  ssh.SSHClient   // Base cluster SSH client (for cleanup)
	BaseKubeconfig     *rest.Config    // Base cluster kubeconfig (for cleanup)
	BaseKubeconfigPath string          // Base cluster kubeconfig path (for cleanup)
	BaseTunnelInfo     *ssh.TunnelInfo // Base cluster tunnel (for cleanup, may be nil if stopped)
	SetupSSHClient     ssh.SSHClient   // Setup node SSH client (for cleanup)
}

// loadClusterConfigFromPath loads and validates a cluster configuration from a specific file path
func loadClusterConfigFromPath(configPath string) (*config.ClusterDefinition, error) {
	// Read the YAML file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Parse YAML directly into ClusterDefinition (has custom UnmarshalYAML for root key)
	var clusterDef config.ClusterDefinition
	if err := yaml.Unmarshal(data, &clusterDef); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Validate the configuration (using the same validation logic as internal/cluster)
	if len(clusterDef.Masters) == 0 {
		return nil, fmt.Errorf("at least one master node is required")
	}

	// Validate DKP parameters
	dkpParams := clusterDef.DKPParameters
	if dkpParams.PodSubnetCIDR == "" {
		return nil, fmt.Errorf("dkpParameters.podSubnetCIDR is required")
	}
	if dkpParams.ServiceSubnetCIDR == "" {
		return nil, fmt.Errorf("dkpParameters.serviceSubnetCIDR is required")
	}
	if dkpParams.ClusterDomain == "" {
		return nil, fmt.Errorf("dkpParameters.clusterDomain is required")
	}
	if dkpParams.RegistryRepo == "" {
		return nil, fmt.Errorf("dkpParameters.registryRepo is required")
	}

	return &clusterDef, nil
}

// CreateTestCluster creates a complete test cluster by performing all necessary steps:
// 1. Loading cluster configuration from YAML
// 2. Connecting to base cluster
// 3. Verifying virtualization module is Ready
// 4. Creating test namespace
// 5. Creating virtual machines
// 6. Gathering VM information
// 7. Establishing SSH connection to setup node
// 8. Installing Docker on setup node
// 9. Preparing and uploading bootstrap config
// 10. Bootstrapping cluster
// 11. Creating NodeGroup for workers
// 12. Verifying cluster is ready
// 13. Adding nodes to cluster
// 14. Enabling and configuring modules
//
// It returns all the resources needed to interact with the test cluster.
// SSH credentials are obtained from environment variables via config functions.
func CreateTestCluster(
	ctx context.Context,
	yamlConfigFilename string,
) (*TestClusterResources, error) {
	fmt.Printf("    ▶️ Step 1: Loading cluster configuration from %s\n", yamlConfigFilename)

	// Get the test file's directory (the caller of CreateTestCluster, which is the test file)
	// runtime.Caller(1) gets the immediate caller (the test file that called CreateTestCluster)
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return nil, fmt.Errorf("failed to determine test file path")
	}
	testDir := filepath.Dir(callerFile)
	yamlConfigPath := filepath.Join(testDir, yamlConfigFilename)

	fmt.Printf("    📁 Test file directory: %s\n", testDir)
	fmt.Printf("    📁 Config file path: %s\n", yamlConfigPath)

	// Step 1: Load cluster configuration from YAML
	// LoadClusterConfig uses runtime.Caller(1) which would get this function, not the test file
	// So we need to load it directly from the path
	clusterDefinition, err := loadClusterConfigFromPath(yamlConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	fmt.Printf("    ✅ Step 1: Cluster configuration loaded successfully from %s\n", yamlConfigPath)

	// Get SSH credentials from environment variables
	sshHost := config.SSHHost
	sshUser := config.SSHUser
	sshKeyPath, err := GetSSHPrivateKeyPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH private key path: %w", err)
	}

	fmt.Printf("    ▶️ Step 2: Connecting to base cluster %s@%s\n", sshUser, sshHost)
	// Step 2: Connect to base cluster
	baseClusterResources, err := ConnectToCluster(ctx, ConnectClusterOptions{
		SSHUser:     sshUser,
		SSHHost:     sshHost,
		SSHKeyPath:  sshKeyPath,
		UseJumpHost: false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to base cluster: %w", err)
	}
	fmt.Printf("    ✅ Step 2: Connected to base cluster successfully\n")

	// Step 3: Verify virtualization module is Ready (can be skipped with SKIP_VIRTUALIZATION_CHECK=true)
	if !config.SkipVirtualizationCheck {
		fmt.Printf("    ▶️ Step 3: Verifying virtualization module is Ready\n")
		moduleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		module, err := deckhouse.GetModule(moduleCtx, baseClusterResources.Kubeconfig, "virtualization")
		cancel()
		if err != nil {
			baseClusterResources.SSHClient.Close()
			baseClusterResources.TunnelInfo.StopFunc()
			return nil, fmt.Errorf("failed to get virtualization module: %w", err)
		}
		if module.Status.Phase != "Ready" {
			baseClusterResources.SSHClient.Close()
			baseClusterResources.TunnelInfo.StopFunc()
			return nil, fmt.Errorf("virtualization module is not Ready (phase: %s)", module.Status.Phase)
		}
		fmt.Printf("    ✅ Step 3: Virtualization module is Ready\n")
	} else {
		fmt.Printf("    ⏭️  Step 3: Skipping virtualization module check (SKIP_VIRTUALIZATION_CHECK=true)\n")
	}

	fmt.Printf("    ▶️ Step 4: Creating test namespace %s\n", config.TestClusterNamespace)
	// Step 4: Create test namespace
	namespaceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	namespace := config.TestClusterNamespace
	_, err = kubernetes.CreateNamespaceIfNotExists(namespaceCtx, baseClusterResources.Kubeconfig, namespace)
	cancel()
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}
	fmt.Printf("    ✅ Step 4: Test namespace created\n")

	fmt.Printf("    ▶️ Step 5: Creating virtual machines (this may take up to 25 minutes)\n")
	// Step 5: Create virtualization client and virtual machines
	virtCtx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	virtClient, err := virtualization.NewClient(virtCtx, baseClusterResources.Kubeconfig)
	if err != nil {
		cancel()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to create virtualization client: %w", err)
	}

	vmNames, vmResources, err := CreateVirtualMachines(virtCtx, virtClient, clusterDefinition)
	cancel()
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to create virtual machines: %w", err)
	}
	fmt.Printf("    ✅ Step 5: Created %d virtual machines: %v\n", len(vmNames), vmNames)

	fmt.Printf("    ▶️ Step 5.1: Waiting for all VMs to become Running (timeout: %v)\n", config.VMsRunningTimeout)
	// Wait for all VMs to become Running (check all VMs in parallel)
	vmWaitCtx, cancel := context.WithTimeout(ctx, config.VMsRunningTimeout)
	defer cancel()

	// Track which VMs are ready
	vmStatus := make(map[string]bool)
	for _, vmName := range vmNames {
		vmStatus[vmName] = false
	}
	totalVMs := len(vmNames)

	allVMsReady := false
	for !allVMsReady {
		select {
		case <-vmWaitCtx.Done():
			// List VMs that are not running
			notRunning := make([]string, 0)
			for _, vmName := range vmNames {
				if !vmStatus[vmName] {
					notRunning = append(notRunning, vmName)
				}
			}
			baseClusterResources.SSHClient.Close()
			baseClusterResources.TunnelInfo.StopFunc()
			return nil, fmt.Errorf("timeout waiting for VMs to become Running. Not ready: %v", notRunning)

		case <-time.After(20 * time.Second):
			readyCount := 0
			for _, vmName := range vmNames {
				if vmStatus[vmName] {
					readyCount++
					continue
				}
				vm, err := virtClient.VirtualMachines().Get(vmWaitCtx, namespace, vmName)
				if err != nil {
					continue
				}
				if vm.Status.Phase == v1alpha2.MachineRunning {
					vmStatus[vmName] = true
					readyCount++
					fmt.Printf("    ✅ VM %s is Running\n", vmName)
				}
			}
			if readyCount == totalVMs {
				allVMsReady = true
			} else {
				fmt.Printf("    ⏳ VMs ready: %d/%d\n", readyCount, totalVMs)
			}
		}
	}
	fmt.Printf("    ✅ Step 5.1: All VMs are Running\n")

	fmt.Printf("    ▶️ Step 6: Gathering VM information\n")
	// Step 6: Gather VM information
	gatherCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = GatherVMInfo(gatherCtx, virtClient, namespace, clusterDefinition, vmResources)
	cancel()
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to gather VM information: %w", err)
	}
	fmt.Printf("    ✅ Step 6: VM information gathered\n")

	fmt.Printf("    ▶️ Step 7: Establishing SSH connection to setup node\n")
	// Step 7: Establish SSH connection to setup node
	setupNode, err := GetSetupNode(clusterDefinition)
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to get setup node: %w", err)
	}
	setupNodeIP := setupNode.IPAddress
	if setupNodeIP == "" {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("setup node IP address is not set")
	}

	setupSSHClient, err := ssh.NewClientWithJumpHost(
		sshUser, sshHost, sshKeyPath, // jump host
		config.VMSSHUser, setupNodeIP, sshKeyPath, // target host
	)
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to create SSH client to setup node: %w", err)
	}
	fmt.Printf("    ✅ Step 7: SSH connection to setup node established\n")

	fmt.Printf("    ▶️ Step 8: Installing Docker on setup node (this may take up to 15 minutes)\n")
	// Step 8: Install Docker on setup node
	dockerCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	err = InstallDocker(dockerCtx, setupSSHClient)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to install Docker on setup node: %w", err)
	}
	fmt.Printf("    ✅ Step 8: Docker installed on setup node\n")

	fmt.Printf("    ▶️ Step 9: Preparing bootstrap configuration\n")
	// Step 9: Prepare bootstrap config
	bootstrapConfig, err := PrepareBootstrapConfig(clusterDefinition)
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to prepare bootstrap config: %w", err)
	}
	fmt.Printf("    ✅ Step 9: Bootstrap configuration prepared\n")

	fmt.Printf("    ▶️ Step 10: Uploading bootstrap files to setup node\n")
	// Step 10: Upload bootstrap files
	uploadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = UploadBootstrapFiles(uploadCtx, setupSSHClient, sshKeyPath, bootstrapConfig)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to upload bootstrap files: %w", err)
	}
	fmt.Printf("    ✅ Step 10: Bootstrap files uploaded\n")

	fmt.Printf("    ▶️ Step 11: Bootstrapping cluster (this may take up to 35 minutes)\n")
	// Step 11: Bootstrap cluster
	firstMasterIP := clusterDefinition.Masters[0].IPAddress
	if firstMasterIP == "" {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("first master IP address is not set")
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, 35*time.Minute)
	err = BootstrapCluster(bootstrapCtx, setupSSHClient, clusterDefinition, bootstrapConfig)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
	}
	fmt.Printf("    ✅ Step 11: Cluster bootstrapped successfully\n")

	fmt.Printf("    ▶️ Step 12: Stopping base cluster tunnel (needed for test cluster tunnel)\n")
	// Step 12: Store base cluster kubeconfig before stopping tunnel (needed for cleanup)
	baseKubeconfig := baseClusterResources.Kubeconfig
	baseKubeconfigPath := baseClusterResources.KubeconfigPath

	// Step 13: Stop base cluster tunnel (needed for test cluster tunnel)
	if baseClusterResources.TunnelInfo != nil && baseClusterResources.TunnelInfo.StopFunc != nil {
		baseClusterResources.TunnelInfo.StopFunc()
	}
	fmt.Printf("    ✅ Step 12: Base cluster tunnel stopped\n")

	fmt.Printf("    ▶️ Step 13: Connecting to test cluster master %s\n", firstMasterIP)
	// Step 14: Connect to test cluster
	testClusterResources, err := ConnectToCluster(ctx, ConnectClusterOptions{
		SSHUser:       sshUser,
		SSHHost:       sshHost,
		SSHKeyPath:    sshKeyPath,
		UseJumpHost:   true,
		TargetUser:    config.VMSSHUser,
		TargetHost:    firstMasterIP,
		TargetKeyPath: sshKeyPath,
	})
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to connect to test cluster: %w", err)
	}
	fmt.Printf("    ✅ Step 13: Connected to test cluster\n")

	fmt.Printf("    ▶️ Step 14: Creating NodeGroup for workers\n")
	// Step 14: Create NodeGroup for workers
	nodegroupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = CreateStaticNodeGroup(nodegroupCtx, testClusterResources.Kubeconfig, "worker")
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to create worker NodeGroup: %w", err)
	}
	fmt.Printf("    ✅ Step 14: NodeGroup for workers created\n")

	fmt.Printf("    ▶️ Step 14.1: Waiting for bootstrap secrets to appear (this may take a few minutes)\n")
	// Step 14.1: Wait for bootstrap secrets to appear after NodeGroup creation
	// The secrets are created by Deckhouse after the NodeGroup is created, so we need to wait
	secretsWaitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	secretNamespace := "d8-cloud-instance-manager"
	secretClient, err := core.NewSecretClient(testClusterResources.Kubeconfig)
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to create secret client: %w", err)
	}

	secretsReady := false
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for !secretsReady {
		select {
		case <-secretsWaitCtx.Done():
			testClusterResources.SSHClient.Close()
			testClusterResources.TunnelInfo.StopFunc()
			setupSSHClient.Close()
			baseClusterResources.SSHClient.Close()
			return nil, fmt.Errorf("timeout waiting for bootstrap secrets to appear")
		case <-ticker.C:
			// Check for both secrets
			_, workerErr := secretClient.Get(secretsWaitCtx, secretNamespace, "manual-bootstrap-for-worker")
			_, masterErr := secretClient.Get(secretsWaitCtx, secretNamespace, "manual-bootstrap-for-master")
			if workerErr == nil && masterErr == nil {
				secretsReady = true
				fmt.Printf("    ✅ Bootstrap secrets are available\n")
			} else {
				fmt.Printf("    ⏳ Waiting for bootstrap secrets... (worker: %v, master: %v)\n",
					workerErr == nil, masterErr == nil)
			}
		}
	}
	fmt.Printf("    ✅ Step 14.1: Bootstrap secrets appeared\n")

	fmt.Printf("    ▶️ Step 15: Verifying cluster is ready (this may take up to 15 minutes)\n")
	// Step 15: Verify cluster is ready
	healthCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	err = CheckClusterHealth(healthCtx, testClusterResources.Kubeconfig)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("cluster is not ready: %w", err)
	}
	fmt.Printf("    ✅ Step 15: Cluster is ready\n")

	fmt.Printf("    ▶️ Step 16: Adding nodes to cluster (timeout: %v)\n", config.NodesReadyTimeout)
	// Step 16: Add nodes to cluster
	nodesCtx, cancel := context.WithTimeout(ctx, config.NodesReadyTimeout)
	err = AddNodesToCluster(nodesCtx, testClusterResources.Kubeconfig, clusterDefinition, sshUser, sshHost, sshKeyPath)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to add nodes to cluster: %w", err)
	}
	fmt.Printf("    ✅ Step 16: Nodes added to cluster\n")

	fmt.Printf("    ▶️ Step 16.1: Waiting for all nodes to become Ready (timeout: %v)\n", config.NodesReadyTimeout)
	// Wait for all nodes to become Ready
	nodesReadyCtx, cancel := context.WithTimeout(ctx, config.NodesReadyTimeout)
	err = WaitForAllNodesReady(nodesReadyCtx, testClusterResources.Kubeconfig, clusterDefinition, config.NodesReadyTimeout)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to wait for nodes to be ready: %w", err)
	}
	fmt.Printf("    ✅ Step 16.1: All nodes are Ready\n")

	fmt.Printf("    ▶️ Step 17: Enabling and configuring modules\n")
	// Step 17: Enable and configure modules
	modulesCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = EnableAndConfigureModules(modulesCtx, testClusterResources.Kubeconfig, clusterDefinition, testClusterResources.SSHClient)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to enable and configure modules: %w", err)
	}
	fmt.Printf("    ✅ Step 17: Modules enabled and configured\n")

	// Set cluster definition and VM resources
	testClusterResources.ClusterDefinition = clusterDefinition
	testClusterResources.VMResources = vmResources
	testClusterResources.BaseClusterClient = baseClusterResources.SSHClient
	testClusterResources.BaseKubeconfig = baseKubeconfig
	testClusterResources.BaseKubeconfigPath = baseKubeconfigPath
	testClusterResources.BaseTunnelInfo = nil // Tunnel was stopped, will be re-established if needed
	testClusterResources.SetupSSHClient = setupSSHClient

	return testClusterResources, nil
}

// WaitForTestClusterReady waits for all modules in the test cluster to become Ready.
// It uses the ModuleDeployTimeout from config.
func WaitForTestClusterReady(ctx context.Context, resources *TestClusterResources) error {
	if resources == nil {
		return fmt.Errorf("resources cannot be nil")
	}
	if resources.Kubeconfig == nil {
		return fmt.Errorf("kubeconfig cannot be nil")
	}
	if resources.ClusterDefinition == nil {
		return fmt.Errorf("cluster definition cannot be nil")
	}

	fmt.Printf("    ▶️ Waiting for all modules to be ready (timeout: %v)\n", config.ModuleDeployTimeout)
	err := WaitForModulesReady(ctx, resources.Kubeconfig, resources.ClusterDefinition, config.ModuleDeployTimeout)
	if err != nil {
		fmt.Printf("    ❌ Failed to wait for modules to be ready: %v\n", err)
		return err
	}
	fmt.Printf("    ✅ All modules are ready\n")
	return nil
}

// CleanupTestCluster cleans up all resources created by CreateTestCluster.
// It performs cleanup in the following order:
// 1. Stop test cluster tunnel and close test cluster SSH client
// 2. Close setup SSH client
// 3. Re-establish base cluster tunnel if needed (for VM cleanup via API)
// 4. Remove setup VM (always removed)
// 5. Remove test cluster VMs if TEST_CLUSTER_CLEANUP is enabled
// 6. Stop base cluster tunnel and close base cluster SSH client
func CleanupTestCluster(ctx context.Context, resources *TestClusterResources) error {
	if resources == nil {
		return nil // Nothing to clean up
	}

	fmt.Printf("    ▶️ Cleanup Step 1: Stopping test cluster tunnel and closing SSH client\n")
	var errs []error

	// Step 1: Stop test cluster tunnel and close test cluster SSH client
	if resources.TunnelInfo != nil && resources.TunnelInfo.StopFunc != nil {
		if err := resources.TunnelInfo.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop test cluster SSH tunnel: %w", err))
			fmt.Printf("    ❌ Failed to stop test cluster SSH tunnel: %v\n", err)
		} else {
			fmt.Printf("    ✅ Test cluster SSH tunnel stopped\n")
		}
	}

	if resources.SSHClient != nil {
		if err := resources.SSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close test cluster SSH client: %w", err))
			fmt.Printf("    ❌ Failed to close test cluster SSH client: %v\n", err)
		} else {
			fmt.Printf("    ✅ Test cluster SSH client closed\n")
		}
	}

	fmt.Printf("    ▶️ Cleanup Step 2: Closing setup SSH client\n")
	// Step 2: Close setup SSH client
	if resources.SetupSSHClient != nil {
		if err := resources.SetupSSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close setup SSH client: %w", err))
			fmt.Printf("    ❌ Failed to close setup SSH client: %v\n", err)
		} else {
			fmt.Printf("    ✅ Setup SSH client closed\n")
		}
	}

	fmt.Printf("    ▶️ Cleanup Step 3: Re-establishing base cluster tunnel for VM cleanup\n")
	// Step 3: Re-establish base cluster tunnel if needed for VM cleanup
	// We need API access to remove VMs, so we need the tunnel
	var baseTunnel *ssh.TunnelInfo
	var cleanupKubeconfig *rest.Config
	if resources.BaseClusterClient != nil && resources.VMResources != nil {
		// Re-establish tunnel if it was stopped (BaseTunnelInfo is nil)
		if resources.BaseTunnelInfo == nil {
			fmt.Printf("    ⏳ Re-establishing base cluster tunnel...\n")
			var tunnelErr error
			baseTunnel, tunnelErr = ssh.EstablishSSHTunnel(context.Background(), resources.BaseClusterClient, "6445")
			if tunnelErr != nil {
				errs = append(errs, fmt.Errorf("failed to re-establish base cluster tunnel for VM cleanup: %w", tunnelErr))
				fmt.Printf("    ❌ Failed to re-establish base cluster tunnel: %v\n", tunnelErr)
			} else {
				fmt.Printf("    ✅ Base cluster tunnel re-established on local port: %d\n", baseTunnel.LocalPort)
				// Update kubeconfig to use the tunnel port
				if resources.BaseKubeconfigPath != "" {
					if updateErr := internalcluster.UpdateKubeconfigPort(resources.BaseKubeconfigPath, baseTunnel.LocalPort); updateErr == nil {
						// Rebuild kubeconfig
						cleanupKubeconfig, _ = clientcmd.BuildConfigFromFlags("", resources.BaseKubeconfigPath)
					}
				}
			}
		} else {
			// Tunnel already exists, use it
			fmt.Printf("    ✅ Base cluster tunnel already exists\n")
			baseTunnel = resources.BaseTunnelInfo
			cleanupKubeconfig = resources.BaseKubeconfig
		}

		// Step 4 & 5: Remove VMs if we have a valid kubeconfig
		if cleanupKubeconfig != nil {
			// Create virtualization client for cleanup
			virtClient, virtErr := virtualization.NewClient(ctx, cleanupKubeconfig)
			if virtErr == nil {
				// Step 4: Remove setup VM (always removed)
				if resources.VMResources.SetupVMName != "" {
					namespace := config.TestClusterNamespace
					fmt.Printf("    ▶️ Cleanup Step 4: Removing setup VM %s\n", resources.VMResources.SetupVMName)
					if removeErr := RemoveVM(ctx, virtClient, namespace, resources.VMResources.SetupVMName); removeErr != nil {
						errs = append(errs, fmt.Errorf("failed to remove setup VM %s: %w", resources.VMResources.SetupVMName, removeErr))
						fmt.Printf("    ❌ Failed to remove setup VM %s: %v\n", resources.VMResources.SetupVMName, removeErr)
					} else {
						fmt.Printf("    ✅ Setup VM %s removed\n", resources.VMResources.SetupVMName)
					}
				}

				// Step 5: Remove test cluster VMs if cleanup is enabled
				if config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True" {
					fmt.Printf("    ▶️ Cleanup Step 5: Removing test cluster VMs (TEST_CLUSTER_CLEANUP is enabled)\n")
					if resources.VMResources != nil && len(resources.VMResources.VMNames) > 0 {
						fmt.Printf("    ⏳ Removing %d VMs: %v\n", len(resources.VMResources.VMNames), resources.VMResources.VMNames)
					}
					if removeErr := RemoveAllVMs(ctx, resources.VMResources); removeErr != nil {
						errs = append(errs, fmt.Errorf("failed to remove test cluster VMs: %w", removeErr))
						fmt.Printf("    ❌ Failed to remove test cluster VMs: %v\n", removeErr)
					} else {
						fmt.Printf("    ✅ Test cluster VMs removed\n")
					}
				} else {
					fmt.Printf("    ⏭️  Cleanup Step 5: Skipping test cluster VM removal (TEST_CLUSTER_CLEANUP is not enabled)\n")
				}
			} else {
				errs = append(errs, fmt.Errorf("failed to create virtualization client for cleanup: %w", virtErr))
				fmt.Printf("    ❌ Failed to create virtualization client for cleanup: %v\n", virtErr)
			}
		} else {
			fmt.Printf("    ⚠️  Warning: Cannot remove VMs - no valid kubeconfig for cleanup\n")
		}
	} else {
		if resources.VMResources == nil {
			fmt.Printf("    ⏭️  Cleanup Step 3-5: Skipping VM cleanup (no VM resources to clean up)\n")
		} else {
			fmt.Printf("    ⚠️  Warning: Cannot remove VMs - base cluster client not available\n")
		}
	}

	fmt.Printf("    ▶️ Cleanup Step 6: Stopping base cluster tunnel and closing SSH client\n")
	// Step 6: Stop base cluster tunnel and close base cluster SSH client
	if baseTunnel != nil && baseTunnel.StopFunc != nil {
		if err := baseTunnel.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop base cluster SSH tunnel: %w", err))
			fmt.Printf("    ❌ Failed to stop base cluster SSH tunnel: %v\n", err)
		} else {
			fmt.Printf("    ✅ Base cluster SSH tunnel stopped\n")
		}
	}

	if resources.BaseClusterClient != nil {
		if err := resources.BaseClusterClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close base cluster SSH client: %w", err))
			fmt.Printf("    ❌ Failed to close base cluster SSH client: %v\n", err)
		} else {
			fmt.Printf("    ✅ Base cluster SSH client closed\n")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	return nil
}

// CheckClusterHealth checks if the deckhouse deployment has 1 pod running with 2/2 containers ready
// in the d8-system namespace, and verifies that bootstrap secrets are available.
// This function is widely used to check cluster health after certain steps.
func CheckClusterHealth(ctx context.Context, kubeconfig *rest.Config) error {
	namespace := "d8-system"
	deploymentName := "deckhouse"

	// Create deployment client
	deploymentClient, err := apps.NewDeploymentClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create deployment client: %w", err)
	}

	// Get the deployment
	deployment, err := deploymentClient.Get(ctx, namespace, deploymentName)
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check if deployment has 1 ready replica (1 pod)
	if deployment.Status.ReadyReplicas != 1 {
		return fmt.Errorf("deployment %s/%s has %d ready replicas, expected 1", namespace, deploymentName, deployment.Status.ReadyReplicas)
	}

	// Create pod client
	podClient, err := core.NewPodClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create pod client: %w", err)
	}

	// Check that bootstrap secrets are available
	secretNamespace := "d8-cloud-instance-manager"
	if err := checkBootstrapSecrets(ctx, kubeconfig, secretNamespace); err != nil {
		return fmt.Errorf("bootstrap secrets not ready: %w", err)
	}

	// Get pods for the deployment using the deployment's selector
	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	pods, err := podClient.ListByLabelSelector(ctx, namespace, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list pods for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check that we have exactly 1 pod
	if len(pods.Items) != 1 {
		return fmt.Errorf("expected 1 pod for deployment %s/%s, found %d", namespace, deploymentName, len(pods.Items))
	}

	// Check the pod is running and has 2/2 containers ready
	pod := pods.Items[0]
	if !podClient.IsRunning(ctx, &pod) {
		return fmt.Errorf("pod %s/%s is not running (phase: %s)", namespace, pod.Name, pod.Status.Phase)
	}

	// Verify the pod has exactly 2 containers
	if len(pod.Spec.Containers) != 2 {
		return fmt.Errorf("pod %s/%s has %d containers, expected 2", namespace, pod.Name, len(pod.Spec.Containers))
	}

	// Check all containers are ready
	if !podClient.AllContainersReady(ctx, &pod) {
		return fmt.Errorf("pod %s/%s does not have all containers ready (expected 2/2 containers ready)", namespace, pod.Name)
	}

	return nil
}

// checkBootstrapSecrets verifies that both bootstrap secrets are available
func checkBootstrapSecrets(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	secretClient, err := core.NewSecretClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create secret client: %w", err)
	}

	// Check for worker bootstrap secret
	_, err = secretClient.Get(ctx, namespace, "manual-bootstrap-for-worker")
	if err != nil {
		// List available secrets for debugging
		secretList, listErr := secretClient.List(ctx, namespace)
		if listErr == nil {
			availableNames := make([]string, 0, len(secretList.Items))
			for _, s := range secretList.Items {
				availableNames = append(availableNames, s.Name)
			}
			return fmt.Errorf("worker bootstrap secret not found: %w. Available secrets in namespace %s: %v", err, namespace, availableNames)
		}
		return fmt.Errorf("worker bootstrap secret not found: %w", err)
	}

	// Check for master bootstrap secret
	_, err = secretClient.Get(ctx, namespace, "manual-bootstrap-for-master")
	if err != nil {
		// List available secrets for debugging
		secretList, listErr := secretClient.List(ctx, namespace)
		if listErr == nil {
			availableNames := make([]string, 0, len(secretList.Items))
			for _, s := range secretList.Items {
				availableNames = append(availableNames, s.Name)
			}
			return fmt.Errorf("master bootstrap secret not found: %w. Available secrets in namespace %s: %v", err, namespace, availableNames)
		}
		return fmt.Errorf("master bootstrap secret not found: %w", err)
	}

	return nil
}

// ConnectClusterOptions defines options for connecting to a cluster
type ConnectClusterOptions struct {
	// Direct connection parameters (used when UseJumpHost is false)
	SSHUser    string
	SSHHost    string
	SSHKeyPath string

	// Jump host parameters (used when UseJumpHost is true)
	UseJumpHost     bool
	JumpHostUser    string // Optional: defaults to SSHUser if empty
	JumpHostHost    string // Optional: defaults to SSHHost if empty
	JumpHostKeyPath string // Optional: defaults to SSHKeyPath if empty
	TargetUser      string // Required when UseJumpHost is true
	TargetHost      string // Required when UseJumpHost is true (IP or hostname)
	TargetKeyPath   string // Optional: defaults to SSHKeyPath if empty
}

// ConnectToCluster establishes SSH connection to a cluster (base or test),
// retrieves kubeconfig, and sets up port forwarding tunnel.
func ConnectToCluster(ctx context.Context, opts ConnectClusterOptions) (*TestClusterResources, error) {
	// Validate required parameters
	if opts.SSHUser == "" {
		return nil, fmt.Errorf("SSHUser cannot be empty")
	}
	if opts.SSHHost == "" {
		return nil, fmt.Errorf("SSHHost cannot be empty")
	}
	if opts.SSHKeyPath == "" {
		return nil, fmt.Errorf("SSHKeyPath cannot be empty")
	}

	var sshClient ssh.SSHClient
	var masterHost string // Host/IP to use for kubeconfig retrieval
	var masterUser string // User to use for kubeconfig retrieval

	if opts.UseJumpHost {
		// Validate jump host parameters
		if opts.TargetHost == "" {
			return nil, fmt.Errorf("TargetHost is required when UseJumpHost is true")
		}
		if opts.TargetUser == "" {
			return nil, fmt.Errorf("TargetUser is required when UseJumpHost is true")
		}

		// Set defaults for jump host parameters
		jumpHostUser := opts.JumpHostUser
		if jumpHostUser == "" {
			jumpHostUser = opts.SSHUser
		}
		jumpHostHost := opts.JumpHostHost
		if jumpHostHost == "" {
			jumpHostHost = opts.SSHHost
		}
		jumpHostKeyPath := opts.JumpHostKeyPath
		if jumpHostKeyPath == "" {
			jumpHostKeyPath = opts.SSHKeyPath
		}
		targetKeyPath := opts.TargetKeyPath
		if targetKeyPath == "" {
			targetKeyPath = opts.SSHKeyPath
		}

		// Create SSH client with jump host (retry with exponential backoff)
		maxRetries := 3
		retryDelay := 2 * time.Second
		var lastErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				// Wait before retry (exponential backoff)
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("context cancelled while retrying SSH connection: %w", ctx.Err())
				case <-time.After(retryDelay):
				}
				retryDelay *= 2 // Exponential backoff
			}

			sshClient, lastErr = ssh.NewClientWithJumpHost(
				jumpHostUser, jumpHostHost, jumpHostKeyPath, // jump host
				opts.TargetUser, opts.TargetHost, targetKeyPath, // target
			)
			if lastErr == nil {
				break // Success
			}
		}
		if lastErr != nil {
			return nil, fmt.Errorf("failed to create SSH client with jump host after %d attempts: %w", maxRetries, lastErr)
		}

		masterHost = opts.TargetHost
		masterUser = opts.TargetUser
	} else {
		// Direct connection (no jump host)
		var err error
		sshClient, err = ssh.NewClient(opts.SSHUser, opts.SSHHost, opts.SSHKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH client: %w", err)
		}

		masterHost = opts.SSHHost
		masterUser = opts.SSHUser
	}

	// Step 2: Establish SSH tunnel with port forwarding 6445:127.0.0.1:6445
	// Use context.Background() for the tunnel so it persists after the function returns
	// The tunnel must remain active for subsequent operations
	tunnelInfo, err := ssh.EstablishSSHTunnel(context.Background(), sshClient, "6445")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("failed to establish SSH tunnel: %w", err)
	}

	// Step 3: Get kubeconfig from cluster master
	_, kubeconfigPath, err := internalcluster.GetKubeconfig(ctx, masterHost, masterUser, opts.SSHKeyPath, sshClient)
	if err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Step 4: Update kubeconfig to use the tunnel port (6445)
	if err := internalcluster.UpdateKubeconfigPort(kubeconfigPath, tunnelInfo.LocalPort); err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to update kubeconfig port: %w", err)
	}

	// Rebuild rest.Config from updated kubeconfig file
	kubeconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to rebuild kubeconfig from file: %w", err)
	}

	// Return resources with active tunnel
	// Note: The test will use Eventually to check cluster health with CheckClusterHealth
	return &TestClusterResources{
		SSHClient:      sshClient,
		Kubeconfig:     kubeconfig,
		KubeconfigPath: kubeconfigPath,
		TunnelInfo:     tunnelInfo,
	}, nil
}
