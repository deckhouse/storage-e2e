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
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/commander"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/testkit"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
)

// extraCommanderValues stores additional values to be passed to Commander cluster creation
// These are merged with values from COMMANDER_VALUES environment variable
var extraCommanderValues map[string]interface{}

// commanderResources stores Commander cluster resources for cleanup
var commanderResources *CommanderClusterResources

// SetExtraCommanderValues sets additional values to be passed when creating a cluster via Commander.
// These values are merged with COMMANDER_VALUES env var (extra values take precedence over env, but prefix is always set).
// Call this before UseCommanderCluster() to customize cluster creation parameters.
func SetExtraCommanderValues(values map[string]interface{}) {
	extraCommanderValues = values
}

// GetCommanderResources returns the stored Commander cluster resources
func GetCommanderResources() *CommanderClusterResources {
	return commanderResources
}

// SetCommanderResources stores Commander cluster resources for later cleanup
func SetCommanderResources(res *CommanderClusterResources) {
	commanderResources = res
}

// ClearCommanderResources clears the stored Commander cluster resources
func ClearCommanderResources() {
	commanderResources = nil
}

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
	logger.Step(1, "Loading cluster configuration from %s", yamlConfigFilename)

	// Get the test file's directory (the caller of CreateTestCluster, which is the test file)
	// runtime.Caller(1) gets the immediate caller (the test file that called CreateTestCluster)
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		return nil, fmt.Errorf("failed to determine test file path")
	}
	testDir := filepath.Dir(callerFile)
	yamlConfigPath := filepath.Join(testDir, yamlConfigFilename)

	logger.Debug("Test file directory: %s", testDir)
	logger.Debug("Config file path: %s", yamlConfigPath)

	// Step 1: Load cluster configuration from YAML
	// LoadClusterConfig uses runtime.Caller(1) which would get this function, not the test file
	// So we need to load it directly from the path
	clusterDefinition, err := loadClusterConfigFromPath(yamlConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	logger.StepComplete(1, "Cluster configuration loaded successfully from %s", yamlConfigPath)

	// Randomize hostnames to avoid SAN initiator collisions.
	// SANs remember iSCSI initiator names keyed by hostname; reusing the same hostnames
	// (master-1, worker-1, etc.) across cluster recreations causes stale initiator mappings.
	// Each node gets its own unique suffix to minimize collision likelihood.
	randomizeHostnames(clusterDefinition)
	logger.Info("Cluster hostnames randomized: masters=%v, workers=%v",
		func() []string {
			names := make([]string, len(clusterDefinition.Masters))
			for i, m := range clusterDefinition.Masters {
				names[i] = m.Hostname
			}
			return names
		}(),
		func() []string {
			names := make([]string, len(clusterDefinition.Workers))
			for i, w := range clusterDefinition.Workers {
				names[i] = w.Hostname
			}
			return names
		}())

	// Get SSH credentials from environment variables
	sshHost := config.SSHHost
	sshUser := config.SSHUser
	sshKeyPath, err := GetSSHPrivateKeyPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH private key path: %w", err)
	}

	logger.Step(2, "Connecting to base cluster %s@%s", sshUser, sshHost)
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
	logger.StepComplete(2, "Connected to base cluster successfully")

	logger.Step(3, "Verifying virtualization module is Ready")
	// Step 3: Verify virtualization module is Ready
	moduleCtx, cancel := context.WithTimeout(ctx, config.ModuleCheckTimeout)
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
	logger.StepComplete(3, "Virtualization module is Ready")

	logger.Step(4, "Creating test namespace %s", config.TestClusterNamespace)
	// Step 4: Create test namespace
	namespaceCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	namespace := config.TestClusterNamespace
	_, err = kubernetes.CreateNamespaceIfNotExists(namespaceCtx, baseClusterResources.Kubeconfig, namespace)
	cancel()
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}
	logger.StepComplete(4, "Test namespace created")

	logger.Step(5, "Creating virtual machines (this may take up to %v)", config.VMCreationTimeout)
	// Step 5: Create virtualization client and virtual machines
	virtCtx, cancel := context.WithTimeout(ctx, config.VMCreationTimeout)
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
	logger.StepComplete(5, "Created %d virtual machines: %v", len(vmNames), vmNames)

	logger.Info("Waiting for all VMs to become Running (this may take up to %v)", config.VMsRunningTimeout)
	// Wait for all VMs to become Running in parallel
	vmWaitCtx, cancel := context.WithTimeout(ctx, config.VMsRunningTimeout)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex // for thread-safe printing
	errChan := make(chan error, len(vmNames))

	for i, vmName := range vmNames {
		wg.Add(1)
		go func(index int, name string) {
			defer wg.Done()

			mu.Lock()
			logger.Progress("Waiting for VM %d/%d: %s", index+1, len(vmNames), name)
			mu.Unlock()

			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-vmWaitCtx.Done():
					errChan <- fmt.Errorf("timeout waiting for VM %s to become Running", name)
					return
				case <-ticker.C:
					vm, err := virtClient.VirtualMachines().Get(vmWaitCtx, namespace, name)
					if err != nil {
						continue
					}
					if vm.Status.Phase == v1alpha2.MachineRunning {
						mu.Lock()
						logger.Success("VM %s is Running", name)
						mu.Unlock()
						return
					}
				}
			}
		}(i, vmName)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check if any errors occurred
	if len(errChan) > 0 {
		err := <-errChan
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, err
	}

	logger.StepComplete(5, "All VMs are Running")

	logger.Step(6, "Gathering VM information")
	// Step 6: Gather VM information
	gatherCtx, cancel := context.WithTimeout(ctx, config.VMInfoTimeout)
	err = GatherVMInfo(gatherCtx, virtClient, namespace, clusterDefinition, vmResources)
	cancel()
	if err != nil {
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to gather VM information: %w", err)
	}
	logger.StepComplete(6, "VM information gathered")

	logger.Step(7, "Establishing SSH connection to setup node")
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
	logger.StepComplete(7, "SSH connection to setup node established")

	logger.Step(8, "Waiting for Docker to be ready on setup node (this may take up to %v)", config.DockerInstallTimeout)
	// Step 8: Wait for Docker to be ready (installed via cloud-init)
	dockerCtx, cancel := context.WithTimeout(ctx, config.DockerInstallTimeout)
	err = WaitForDockerReady(dockerCtx, setupSSHClient)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("Docker is not ready on setup node: %w", err)
	}
	logger.StepComplete(8, "Docker is ready on setup node")

	logger.Step(9, "Preparing bootstrap configuration")
	// Step 9: Prepare bootstrap config
	bootstrapConfig, err := PrepareBootstrapConfig(clusterDefinition)
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to prepare bootstrap config: %w", err)
	}
	logger.StepComplete(9, "Bootstrap configuration prepared")

	logger.Step(10, "Uploading bootstrap files to setup node")
	// Step 10: Upload bootstrap files
	uploadCtx, cancel := context.WithTimeout(ctx, config.BootstrapUploadTimeout)
	err = UploadBootstrapFiles(uploadCtx, setupSSHClient, sshKeyPath, bootstrapConfig)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to upload bootstrap files: %w", err)
	}
	logger.StepComplete(10, "Bootstrap files uploaded")

	logger.Step(11, "Bootstrapping cluster (this may take up to %v)", config.DKPDeployTimeout)
	// Step 11: Bootstrap cluster
	firstMasterIP := clusterDefinition.Masters[0].IPAddress
	if firstMasterIP == "" {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("first master IP address is not set")
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, config.DKPDeployTimeout)
	err = BootstrapCluster(bootstrapCtx, setupSSHClient, clusterDefinition, bootstrapConfig)
	cancel()
	if err != nil {
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		baseClusterResources.TunnelInfo.StopFunc()
		return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
	}
	logger.StepComplete(11, "Cluster bootstrapped successfully")

	// Store base cluster kubeconfig (tunnel stays open for later use)
	baseKubeconfig := baseClusterResources.Kubeconfig
	baseKubeconfigPath := baseClusterResources.KubeconfigPath
	baseTunnelInfo := baseClusterResources.TunnelInfo

	logger.Step(13, "Connecting to test cluster master %s", firstMasterIP)
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
	logger.StepComplete(13, "Connected to test cluster")

	logger.Step(14, "Creating NodeGroup for workers")
	// Step 14: Create NodeGroup for workers
	nodegroupCtx, cancel := context.WithTimeout(ctx, config.NodeGroupTimeout)
	err = kubernetes.CreateStaticNodeGroup(nodegroupCtx, testClusterResources.Kubeconfig, "worker")
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to create worker NodeGroup: %w", err)
	}
	logger.StepComplete(14, "NodeGroup for workers created")

	logger.Debug("Waiting for master and worker bootstrap secrets to appear")
	// Step 14.1: Wait for bootstrap secrets to appear after NodeGroup creation
	// The secrets are created by Deckhouse after the NodeGroup is created, so we need to wait
	secretsWaitCtx, cancel := context.WithTimeout(ctx, config.SecretsWaitTimeout)
	defer cancel()
	secretNamespace := "d8-cloud-instance-manager"
	clientset, err := kubernetes.NewClientsetWithRetry(secretsWaitCtx, testClusterResources.Kubeconfig)
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
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
			_, workerErr := clientset.CoreV1().Secrets(secretNamespace).Get(secretsWaitCtx, "manual-bootstrap-for-worker", metav1.GetOptions{})
			_, masterErr := clientset.CoreV1().Secrets(secretNamespace).Get(secretsWaitCtx, "manual-bootstrap-for-master", metav1.GetOptions{})
			if workerErr == nil && masterErr == nil {
				secretsReady = true
				logger.Debug("Both master and worker bootstrap secrets are available")
			} else {
				logger.Progress("Waiting for bootstrap secrets... (worker: %v, master: %v)",
					workerErr == nil, masterErr == nil)
			}
		}
	}

	logger.Step(15, "Verifying cluster is ready (this may take up to %v)", config.ClusterHealthTimeout)
	// Step 15: Verify cluster is ready
	healthCtx, cancel := context.WithTimeout(ctx, config.ClusterHealthTimeout)
	err = CheckClusterHealth(healthCtx, testClusterResources.Kubeconfig)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("cluster is not ready: %w", err)
	}
	logger.StepComplete(15, "Cluster is ready")

	logger.Step(16, "Adding nodes to the cluster")
	// Step 16: Add nodes to cluster
	nodesCtx, cancel := context.WithTimeout(ctx, config.NodesReadyTimeout)
	err = AddNodesToCluster(nodesCtx, testClusterResources.Kubeconfig, clusterDefinition, sshUser, sshHost, sshKeyPath)
	cancel()
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to add nodes to the cluster: %w", err)
	}
	logger.StepComplete(16, "Nodes added to cluster")

	logger.Info("Waiting for all nodes to become Ready (this may take up to %v)", config.NodesReadyTimeout)
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
	logger.Success("All nodes are Ready")

	logger.Step(17, "Enabling and configuring modules")
	// Step 17: Enable and configure modules
	// Note: EnableAndConfigureModules has internal timeouts per module (ModuleDeployTimeout)
	// We use the parent context which has ClusterCreationTimeout
	err = kubernetes.EnableAndConfigureModules(ctx, testClusterResources.Kubeconfig, clusterDefinition, testClusterResources.SSHClient)
	if err != nil {
		testClusterResources.SSHClient.Close()
		testClusterResources.TunnelInfo.StopFunc()
		setupSSHClient.Close()
		baseClusterResources.SSHClient.Close()
		return nil, fmt.Errorf("failed to enable and configure modules: %w", err)
	}
	logger.StepComplete(17, "Modules are enabled, configured and Ready")

	// Set cluster definition and VM resources
	testClusterResources.ClusterDefinition = clusterDefinition
	testClusterResources.VMResources = vmResources
	testClusterResources.BaseClusterClient = baseClusterResources.SSHClient
	testClusterResources.BaseKubeconfig = baseKubeconfig
	testClusterResources.BaseKubeconfigPath = baseKubeconfigPath
	testClusterResources.BaseTunnelInfo = baseTunnelInfo
	testClusterResources.SetupSSHClient = setupSSHClient

	return testClusterResources, nil
}

// UseExistingCluster connects to an existing cluster without creating new VMs.
// It establishes SSH connection, retrieves kubeconfig, and acquires a cluster lock.
// The lock ensures that only one test uses the cluster at a time.
// SSH credentials are obtained from environment variables via config functions.
//
// If SSH_JUMP_HOST is set, the connection will go through the jump host first.
// This is useful for clusters behind a bastion/jump host.
//
// If the cluster is already locked by another test, this function will return an error.
// The lock is automatically released when CleanupExistingCluster is called.
func UseExistingCluster(ctx context.Context) (*TestClusterResources, error) {
	logger.Step(1, "Connecting to existing cluster")

	// Get SSH credentials from environment variables
	sshHost := config.SSHHost
	sshUser := config.SSHUser
	sshKeyPath, err := GetSSHPrivateKeyPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH private key path: %w", err)
	}

	// Check if jump host is configured
	var clusterResources *TestClusterResources
	if config.SSHJumpHost != "" {
		// Use jump host to connect to the cluster
		jumpUser := config.SSHJumpUser
		if jumpUser == "" {
			jumpUser = sshUser // Default to SSH_USER if SSH_JUMP_USER is not set
		}

		// Determine jump host key path
		jumpKeyPath := config.SSHJumpKeyPath
		if jumpKeyPath == "" {
			jumpKeyPath = sshKeyPath // Default to SSH_PRIVATE_KEY if SSH_JUMP_KEY_PATH is not set
		}

		logger.Info("Using jump host %s@%s to connect to target %s@%s", jumpUser, config.SSHJumpHost, sshUser, sshHost)
		clusterResources, err = ConnectToCluster(ctx, ConnectClusterOptions{
			SSHUser:       jumpUser,
			SSHHost:       config.SSHJumpHost,
			SSHKeyPath:    jumpKeyPath,
			UseJumpHost:   true,
			TargetUser:    sshUser,
			TargetHost:    sshHost,
			TargetKeyPath: sshKeyPath,
		})
	} else {
		// Direct connection (no jump host)
		clusterResources, err = ConnectToCluster(ctx, ConnectClusterOptions{
			SSHUser:     sshUser,
			SSHHost:     sshHost,
			SSHKeyPath:  sshKeyPath,
			UseJumpHost: false,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to existing cluster: %w", err)
	}
	logger.StepComplete(1, "Connected to existing cluster successfully")

	logger.Step(2, "Acquiring cluster lock")
	// Acquire cluster lock to ensure exclusive access
	// Use a descriptive test name from environment or generate one
	testName := config.TestClusterNamespace
	if testName == "" {
		testName = fmt.Sprintf("e2e-test-%d", time.Now().UnixNano())
	}

	err = AcquireClusterLock(ctx, clusterResources.Kubeconfig, testName)
	if err != nil {
		// Cleanup resources if we can't acquire the lock
		if clusterResources.TunnelInfo != nil && clusterResources.TunnelInfo.StopFunc != nil {
			clusterResources.TunnelInfo.StopFunc()
		}
		if clusterResources.SSHClient != nil {
			clusterResources.SSHClient.Close()
		}
		return nil, fmt.Errorf("failed to acquire cluster lock: %w", err)
	}
	logger.StepComplete(2, "Cluster lock acquired")

	logger.Step(3, "Verifying cluster health (with retries)")
	// Verify cluster health with retries - cluster may need time to stabilize
	const maxHealthRetries = 10
	const healthRetryInterval = 30 * time.Second
	var healthErr error
	for attempt := 1; attempt <= maxHealthRetries; attempt++ {
		healthCtx, cancel := context.WithTimeout(ctx, config.ClusterHealthTimeout)
		healthErr = CheckClusterHealth(healthCtx, clusterResources.Kubeconfig, CheckClusterHealthOptions{
			CheckBootstrapSecrets: false, // Existing clusters don't need bootstrap secrets
		})
		cancel()

		if healthErr == nil {
			break
		}

		if attempt < maxHealthRetries {
			logger.Warn("Health check attempt %d/%d failed: %v. Retrying in %v...", attempt, maxHealthRetries, healthErr, healthRetryInterval)
			select {
			case <-ctx.Done():
				healthErr = fmt.Errorf("context cancelled during health check retries: %w", ctx.Err())
				break
			case <-time.After(healthRetryInterval):
				// Continue to next attempt
			}
		}
	}

	if healthErr != nil {
		// Release the lock and cleanup on failure
		_ = ReleaseClusterLock(ctx, clusterResources.Kubeconfig)
		if clusterResources.TunnelInfo != nil && clusterResources.TunnelInfo.StopFunc != nil {
			clusterResources.TunnelInfo.StopFunc()
		}
		if clusterResources.SSHClient != nil {
			clusterResources.SSHClient.Close()
		}
		return nil, fmt.Errorf("cluster health check failed after %d attempts: %w", maxHealthRetries, healthErr)
	}
	logger.StepComplete(3, "Cluster is healthy")

	logger.Success("Existing cluster is ready for use")
	return clusterResources, nil
}

// CleanupExistingCluster releases the cluster lock and closes connections.
// This should be called after using an existing cluster with UseExistingCluster.
// Note: Stress namespace cleanup should be done by the caller before calling this function.
func CleanupExistingCluster(ctx context.Context, resources *TestClusterResources) error {
	if resources == nil {
		return nil
	}

	var errs []error

	logger.Step(1, "Releasing cluster lock")
	// Release the cluster lock
	if resources.Kubeconfig != nil {
		if err := ReleaseClusterLock(ctx, resources.Kubeconfig); err != nil {
			errs = append(errs, fmt.Errorf("failed to release cluster lock: %w", err))
			logger.Error("Failed to release cluster lock: %v", err)
		} else {
			logger.Success("Cluster lock released")
		}
	}

	logger.Step(2, "Closing connections")
	// Stop tunnel
	if resources.TunnelInfo != nil && resources.TunnelInfo.StopFunc != nil {
		if err := resources.TunnelInfo.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop SSH tunnel: %w", err))
			logger.Error("Failed to stop SSH tunnel: %v", err)
		} else {
			logger.Success("SSH tunnel stopped")
		}
	}

	// Close SSH client
	if resources.SSHClient != nil {
		if err := resources.SSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close SSH client: %w", err))
			logger.Error("Failed to close SSH client: %v", err)
		} else {
			logger.Success("SSH client closed")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	logger.Success("Existing cluster cleanup completed")
	return nil
}

// CommanderClusterResources holds resources for a Commander-managed cluster
type CommanderClusterResources struct {
	*TestClusterResources
	CommanderClient *commander.Client
	ClusterName     string
	CreatedByUs     bool // True if we created the cluster, false if we used existing
}

// UseCommanderCluster connects to or creates a cluster via Deckhouse Commander.
// It performs the following steps:
// 1. Creates a Commander client
// 2. Checks if the cluster exists in Commander
// 3. If COMMANDER_CREATE_IF_NOT_EXISTS is true and cluster doesn't exist, creates it
// 4. Waits for the cluster to become ready
// 5. Retrieves kubeconfig and connection info
// 6. Establishes SSH connection to the cluster
// 7. Acquires cluster lock
//
// Environment variables used:
// - COMMANDER_URL: URL of the Commander API
// - COMMANDER_TOKEN: API token for authentication
// - COMMANDER_CLUSTER_NAME: Name of the cluster to use/create
// - COMMANDER_TEMPLATE_NAME: Template for creating new clusters
// - COMMANDER_TEMPLATE_VERSION: Version of the template (optional)
// - COMMANDER_CREATE_IF_NOT_EXISTS: Whether to create cluster if it doesn't exist
// - COMMANDER_WAIT_TIMEOUT: Timeout for waiting for cluster to become ready
func UseCommanderCluster(ctx context.Context) (*CommanderClusterResources, error) {
	logger.Step(1, "Connecting to Deckhouse Commander")

	// Determine auth method from environment
	// Default is X-Auth-Token as per Commander documentation
	// See: https://deckhouse.io/modules/commander/stable/integration_api.html
	authMethod := commander.AuthMethod(config.CommanderAuthMethod)
	if authMethod == "" {
		authMethod = commander.AuthMethodXAuthToken
	}

	// Determine API prefix from environment
	apiPrefix := config.CommanderAPIPrefix
	if apiPrefix == "" {
		apiPrefix = config.CommanderAPIPrefixDefaultValue
	}

	// Create Commander client with options from environment
	clientOpts := commander.ClientOptions{
		InsecureSkipTLSVerify: config.CommanderInsecureSkipTLSVerify == "true",
		CACertPath:            config.CommanderCACert,
		AuthMethod:            authMethod,
		AuthUser:              config.CommanderAuthUser,
		APIPrefix:             apiPrefix,
	}

	if clientOpts.InsecureSkipTLSVerify {
		logger.Warn("TLS certificate verification is disabled (COMMANDER_INSECURE_SKIP_TLS_VERIFY=true)")
	} else if clientOpts.CACertPath != "" {
		logger.Debug("Using custom CA certificate: %s", clientOpts.CACertPath)
	}

	logger.Debug("Using auth method: %s", authMethod)
	logger.Debug("Using API prefix: %s", apiPrefix)

	commanderClient, err := commander.NewClientWithOptions(config.CommanderURL, config.CommanderToken, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Commander client: %w", err)
	}
	logger.StepComplete(1, "Connected to Commander at %s", config.CommanderURL)

	clusterName := config.CommanderClusterName
	// Append random suffix to ensure unique node names across cluster recreations.
	// Commander templates use the cluster name as the "prefix" for node naming,
	// so randomizing it prevents SAN initiator name collisions.
	clusterSuffix := GenerateRandomSuffix(5)
	clusterName = clusterName + "-" + clusterSuffix
	logger.Info("Commander cluster name randomized: %s", clusterName)
	createdByUs := false

	logger.Step(2, "Checking if cluster '%s' exists in Commander", clusterName)
	// Check if cluster exists
	cluster, err := commanderClient.GetCluster(ctx, clusterName)
	if err != nil {
		if err == commander.ErrClusterNotFound {
			// Cluster doesn't exist - check if we should create it
			if config.CommanderCreateIfNotExists != "true" {
				return nil, fmt.Errorf("cluster '%s' not found in Commander and COMMANDER_CREATE_IF_NOT_EXISTS is not 'true'", clusterName)
			}

			logger.Info("Cluster '%s' not found, creating from template '%s'", clusterName, config.CommanderTemplateName)

			// Create cluster from template
			cluster, err = createCommanderCluster(ctx, commanderClient, clusterName)
			if err != nil {
				return nil, fmt.Errorf("failed to create cluster in Commander: %w", err)
			}
			createdByUs = true
			logger.Success("Cluster '%s' creation initiated", clusterName)
		} else {
			return nil, fmt.Errorf("failed to get cluster from Commander: %w", err)
		}
	} else {
		logger.StepComplete(2, "Cluster '%s' found in Commander (phase: %s)", clusterName, cluster.Status.Phase)
	}

	// Wait for cluster to become ready if not already
	if cluster.Status.Phase != commander.ClusterPhaseReady {
		logger.Step(3, "Waiting for cluster '%s' to become ready", clusterName)

		// Parse timeout from config
		timeout, err := time.ParseDuration(config.CommanderWaitTimeout)
		if err != nil {
			timeout = 30 * time.Minute // Default timeout
		}

		cluster, err = commanderClient.WaitForClusterReady(ctx, clusterName, timeout)
		if err != nil {
			return nil, fmt.Errorf("failed to wait for cluster to become ready: %w", err)
		}
		logger.StepComplete(3, "Cluster '%s' is ready", clusterName)
	}

	logger.Step(4, "Retrieving cluster connection information from Commander")

	// Get connection info from Commander (includes SSH host from connection_hosts.masters)
	connInfo, connErr := commanderClient.GetClusterConnectionInfo(ctx, clusterName)
	if connErr != nil {
		return nil, fmt.Errorf("failed to get cluster connection info: %w", connErr)
	}

	if connInfo.SSHHost == "" {
		return nil, fmt.Errorf("no SSH connection info available from Commander (connection_hosts.masters is empty)")
	}

	logger.Debug("Cluster SSH host from Commander: %s@%s", connInfo.SSHUser, connInfo.SSHHost)
	logger.StepComplete(4, "Retrieved connection info from Commander")

	// Get kubeconfig via SSH - use connection_hosts.masters for target and jump host from env
	// Commander doesn't provide kubeconfig via API, we always get it via SSH using sudo
	sshKeyPath, keyErr := GetSSHPrivateKeyPath()
	if keyErr != nil {
		return nil, fmt.Errorf("SSH key not available: %w", keyErr)
	}

	targetHost := connInfo.SSHHost
	// Use SSH_USER if set, otherwise fall back to user from Commander, then to VMSSHUser
	targetUser := config.SSHUser
	if targetUser == "" {
		targetUser = connInfo.SSHUser
	}
	if targetUser == "" {
		targetUser = config.VMSSHUser
	}

	logger.Step(5, "Getting kubeconfig via SSH to %s@%s", targetUser, targetHost)

	var clusterResources *TestClusterResources

	// Use jump host from environment variables
	if config.SSHJumpHost != "" {
		jumpUser := config.SSHJumpUser
		if jumpUser == "" {
			jumpUser = config.SSHUser
		}
		jumpKeyPath := config.SSHJumpKeyPath
		if jumpKeyPath == "" {
			jumpKeyPath = sshKeyPath
		}

		logger.Info("Using jump host %s@%s to connect to target %s@%s",
			jumpUser, config.SSHJumpHost, targetUser, targetHost)

		clusterResources, err = ConnectToCluster(ctx, ConnectClusterOptions{
			SSHUser:       jumpUser,
			SSHHost:       config.SSHJumpHost,
			SSHKeyPath:    jumpKeyPath,
			UseJumpHost:   true,
			TargetUser:    targetUser,
			TargetHost:    targetHost,
			TargetKeyPath: sshKeyPath,
		})
	} else {
		// Direct connection without jump host
		clusterResources, err = ConnectToCluster(ctx, ConnectClusterOptions{
			SSHUser:     targetUser,
			SSHHost:     targetHost,
			SSHKeyPath:  sshKeyPath,
			UseJumpHost: false,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster via SSH: %w", err)
	}

	logger.StepComplete(5, "Kubeconfig retrieved via SSH")

	logger.Step(6, "Acquiring cluster lock")
	// Acquire cluster lock
	testName := config.TestClusterNamespace
	if testName == "" {
		testName = fmt.Sprintf("e2e-test-%d", time.Now().UnixNano())
	}

	err = AcquireClusterLock(ctx, clusterResources.Kubeconfig, testName)
	if err != nil {
		// Cleanup on failure
		if clusterResources.SSHClient != nil {
			clusterResources.SSHClient.Close()
		}
		if clusterResources.TunnelInfo != nil && clusterResources.TunnelInfo.StopFunc != nil {
			clusterResources.TunnelInfo.StopFunc()
		}
		return nil, fmt.Errorf("failed to acquire cluster lock: %w", err)
	}
	logger.StepComplete(6, "Cluster lock acquired")

	logger.Step(7, "Verifying cluster health (with retries)")
	// Verify cluster health with retries - cluster may need time to stabilize
	const maxHealthRetries = 10
	const healthRetryInterval = 30 * time.Second
	var healthErr error
	for attempt := 1; attempt <= maxHealthRetries; attempt++ {
		healthCtx, cancel := context.WithTimeout(ctx, config.ClusterHealthTimeout)
		healthErr = CheckClusterHealth(healthCtx, clusterResources.Kubeconfig, CheckClusterHealthOptions{
			CheckBootstrapSecrets: false, // Commander clusters don't need bootstrap secrets check
		})
		cancel()

		if healthErr == nil {
			break
		}

		if attempt < maxHealthRetries {
			logger.Warn("Health check attempt %d/%d failed: %v. Retrying in %v...", attempt, maxHealthRetries, healthErr, healthRetryInterval)
			select {
			case <-ctx.Done():
				healthErr = fmt.Errorf("context cancelled during health check retries: %w", ctx.Err())
				break
			case <-time.After(healthRetryInterval):
				// Continue to next attempt
			}
		}
	}

	if healthErr != nil {
		// Cleanup on failure
		_ = ReleaseClusterLock(ctx, clusterResources.Kubeconfig)
		if clusterResources.SSHClient != nil {
			clusterResources.SSHClient.Close()
		}
		if clusterResources.TunnelInfo != nil && clusterResources.TunnelInfo.StopFunc != nil {
			clusterResources.TunnelInfo.StopFunc()
		}
		return nil, fmt.Errorf("cluster health check failed after %d attempts: %w", maxHealthRetries, healthErr)
	}
	logger.StepComplete(7, "Cluster is healthy")

	logger.Success("Commander cluster '%s' is ready for use", clusterName)

	return &CommanderClusterResources{
		TestClusterResources: clusterResources,
		CommanderClient:      commanderClient,
		ClusterName:          clusterName,
		CreatedByUs:          createdByUs,
	}, nil
}

// createCommanderCluster creates a new cluster in Commander from a template
func createCommanderCluster(ctx context.Context, client *commander.Client, name string) (*commander.Cluster, error) {
	templateName := config.CommanderTemplateName

	// First, list available templates to find the one we need
	logger.Debug("Checking for template '%s'", templateName)
	templates, listErr := client.ListClusterTemplates(ctx)
	if listErr != nil {
		logger.Debug("Failed to list templates: %v", listErr)
		return nil, fmt.Errorf("failed to list templates: %w", listErr)
	}

	if len(templates) > 0 {
		logger.Debug("Available templates (%d):", len(templates))
		for _, t := range templates {
			logger.Debug("  - %s (id: %s, versions: %d)", t.Name, t.ID, len(t.Versions))
		}
	} else {
		logger.Debug("No templates found")
		return nil, fmt.Errorf("no templates available in Commander")
	}

	// Find template by name
	var foundTemplate *commander.ClusterTemplateResponse
	for i, t := range templates {
		if t.Name == templateName {
			foundTemplate = &templates[i]
			break
		}
	}

	if foundTemplate == nil {
		availableNames := make([]string, len(templates))
		for i, t := range templates {
			availableNames[i] = t.Name
		}
		return nil, fmt.Errorf("template '%s' not found. Available templates: %v", templateName, availableNames)
	}

	logger.Debug("Template '%s' found (id: %s)", templateName, foundTemplate.ID)
	logger.Debug("Current cluster template version id: %s", foundTemplate.CurrentClusterTemplateVersionID)

	// Get template versions from cluster_template_versions field
	versions := foundTemplate.ClusterTemplateVersions
	// Fallback to legacy 'versions' field if cluster_template_versions is empty
	if len(versions) == 0 {
		versions = foundTemplate.Versions
	}

	if len(versions) > 0 {
		logger.Debug("Available template versions (%d):", len(versions))
		for _, v := range versions {
			logger.Debug("  - name: %s, id: %s", v.Name, v.ID)
		}
	} else {
		logger.Debug("No versions found in template response")
	}

	// Find the template version ID
	var templateVersionID string
	if config.CommanderTemplateVersion != "" {
		// Find specific version by name or ID
		for _, v := range versions {
			if v.Name == config.CommanderTemplateVersion || v.ID == config.CommanderTemplateVersion {
				templateVersionID = v.ID
				break
			}
		}
		if templateVersionID == "" {
			availableVersions := make([]string, len(versions))
			for i, v := range versions {
				availableVersions[i] = fmt.Sprintf("name: %s (id: %s)", v.Name, v.ID)
			}
			return nil, fmt.Errorf("template version '%s' not found. Available versions: %v", config.CommanderTemplateVersion, availableVersions)
		}
		logger.Debug("Using specified template version: %s", templateVersionID)
	} else if foundTemplate.CurrentClusterTemplateVersionID != "" {
		// Use current_cluster_template_version_id if available
		templateVersionID = foundTemplate.CurrentClusterTemplateVersionID
		logger.Debug("Using current template version id: %s", templateVersionID)
	} else if len(versions) > 0 {
		// Use the first version
		templateVersionID = versions[0].ID
		logger.Debug("Using first available template version: name=%s, id=%s", versions[0].Name, templateVersionID)
	} else {
		return nil, fmt.Errorf("template '%s' has no versions available", templateName)
	}

	logger.Debug("Creating cluster '%s' with template version id: %s", name, templateVersionID)

	// Get registry ID if COMMANDER_REGISTRY_NAME is set
	var registryID string
	if config.CommanderRegistryName != "" {
		logger.Debug("Looking up registry '%s' to get registry_id", config.CommanderRegistryName)
		registry, err := client.GetRegistryByName(ctx, config.CommanderRegistryName)
		if err != nil {
			return nil, fmt.Errorf("failed to get registry '%s': %w", config.CommanderRegistryName, err)
		}
		registryID = registry.ID
		logger.Debug("Resolved registry '%s' to registry_id: %s", config.CommanderRegistryName, registryID)
	}

	// Build values for cluster creation (template input parameters)
	// See: https://deckhouse.io/modules/commander/stable/integration_api.html
	values, err := buildCommanderValues(name)
	if err != nil {
		return nil, fmt.Errorf("failed to build values: %w", err)
	}

	if len(values) > 0 {
		logger.Debug("Using values for cluster creation: %v", values)
	}

	// Create cluster using Commander API
	clusterResp, err := client.CreateClusterFromTemplate(ctx, name, templateVersionID, registryID, values)
	if err != nil {
		return nil, err
	}

	// Convert ClusterResponse to Cluster for compatibility
	cluster := &commander.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterResp.Name,
		},
		Status: commander.ClusterStatus{
			Phase:       commander.ClusterPhase(clusterResp.Status),
			Message:     clusterResp.Message,
			APIEndpoint: clusterResp.APIEndpoint,
		},
	}

	// Also try to use Phase if Status is empty
	if cluster.Status.Phase == "" && clusterResp.Phase != "" {
		cluster.Status.Phase = commander.ClusterPhase(clusterResp.Phase)
	}

	return cluster, nil
}

// buildCommanderValues builds the values map for cluster creation from environment variables
// See: https://deckhouse.io/modules/commander/stable/integration_api.html
// Values contain template input parameters like releaseChannel, kubeVersion, slot, prefix, etc.
func buildCommanderValues(clusterName string) (map[string]interface{}, error) {
	values := make(map[string]interface{})

	// If COMMANDER_VALUES is set, parse it as JSON and merge
	if config.CommanderInputValues != "" {
		if err := json.Unmarshal([]byte(config.CommanderInputValues), &values); err != nil {
			return nil, fmt.Errorf("failed to parse COMMANDER_VALUES as JSON: %w", err)
		}
	}

	// Merge extra values set programmatically (extra values take precedence)
	for k, v := range extraCommanderValues {
		values[k] = v
	}

	// Always set prefix to cluster name (required by Commander)
	values["prefix"] = clusterName

	return values, nil
}

// saveCommanderKubeconfig saves the kubeconfig content to a temporary file
func saveCommanderKubeconfig(clusterName, kubeconfigContent string) (string, error) {
	// Determine the temp directory path
	_, callerFile, _, ok := runtime.Caller(2)
	if !ok {
		return "", fmt.Errorf("failed to get caller file information")
	}
	testFileName := strings.TrimSuffix(filepath.Base(callerFile), filepath.Ext(callerFile))

	callerDir := filepath.Dir(callerFile)
	repoRootPath := filepath.Join(callerDir, "..", "..")
	repoRoot, err := filepath.Abs(repoRootPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve repo root path: %w", err)
	}

	tempDir := filepath.Join(repoRoot, "temp", testFileName)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	kubeconfigPath := filepath.Join(tempDir, fmt.Sprintf("kubeconfig-%s.yaml", clusterName))
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return kubeconfigPath, nil
}

// CleanupCommanderCluster releases resources and optionally deletes the cluster from Commander.
// If the cluster was created by us (CreatedByUs is true) and TEST_CLUSTER_CLEANUP is enabled,
// the cluster will be deleted from Commander.
// Note: Stress namespace cleanup should be done by the caller before calling this function.
func CleanupCommanderCluster(ctx context.Context, resources *CommanderClusterResources) error {
	if resources == nil {
		return nil
	}

	var errs []error

	logger.Step(1, "Releasing cluster lock")
	// Release cluster lock
	if resources.Kubeconfig != nil {
		if err := ReleaseClusterLock(ctx, resources.Kubeconfig); err != nil {
			errs = append(errs, fmt.Errorf("failed to release cluster lock: %w", err))
			logger.Error("Failed to release cluster lock: %v", err)
		} else {
			logger.Success("Cluster lock released")
		}
	}

	logger.Step(2, "Closing connections")
	// Close SSH client
	if resources.SSHClient != nil {
		if err := resources.SSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close SSH client: %w", err))
			logger.Error("Failed to close SSH client: %v", err)
		} else {
			logger.Success("SSH client closed")
		}
	}

	// Stop tunnel if exists
	if resources.TunnelInfo != nil && resources.TunnelInfo.StopFunc != nil {
		if err := resources.TunnelInfo.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop SSH tunnel: %w", err))
			logger.Error("Failed to stop SSH tunnel: %v", err)
		} else {
			logger.Success("SSH tunnel stopped")
		}
	}

	// Delete cluster from Commander if we created it
	// For Commander-created clusters: delete by default unless TEST_CLUSTER_CLEANUP=false
	cleanupDisabled := config.TestClusterCleanup == "false" || config.TestClusterCleanup == "False"
	if resources.CreatedByUs && !cleanupDisabled {
		logger.Step(3, "Deleting cluster '%s' from Commander", resources.ClusterName)
		if err := resources.CommanderClient.DeleteCluster(ctx, resources.ClusterName); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete cluster from Commander: %w", err))
			logger.Error("Failed to delete cluster from Commander: %v", err)
		} else {
			logger.Success("Cluster '%s' deleted from Commander", resources.ClusterName)
		}
	} else if !resources.CreatedByUs {
		logger.Skip("Skipping cluster deletion (cluster was not created by us)")
	} else {
		logger.Skip("Skipping cluster deletion (TEST_CLUSTER_CLEANUP=false)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	logger.Success("Commander cluster cleanup completed")
	return nil
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

	logger.Info("Waiting for all modules to become Ready (this may take up to %v)", config.ModuleDeployTimeout)
	err := kubernetes.WaitForModulesReady(ctx, resources.Kubeconfig, resources.ClusterDefinition, config.ModuleDeployTimeout)
	if err != nil {
		logger.Error("Failed to wait for modules to be ready: %v", err)
		return err
	}
	logger.StepComplete(18, "All modules are ready")
	return nil
}

// configureExtendedTimeouts configures rest.Config with extended timeouts for tunnel-based connections
// This helps prevent timeouts when API server is under load or network latency is high
func configureExtendedTimeouts(config *rest.Config) {
	// Increase overall request timeout from default 30s to 2 minutes
	config.Timeout = 2 * time.Minute

	// Wrap the transport to extend timeouts without breaking authentication
	// We preserve the existing WrapTransport if any, and wrap on top of it
	originalWrapTransport := config.WrapTransport
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		// First apply original wrapper if it exists
		if originalWrapTransport != nil {
			rt = originalWrapTransport(rt)
		}

		// Then modify transport timeouts if it's an http.Transport
		if httpTransport, ok := rt.(*http.Transport); ok {
			// Clone transport to avoid modifying shared instances
			clonedTransport := httpTransport.Clone()
			clonedTransport.TLSHandshakeTimeout = 30 * time.Second   // Extended from default 10s
			clonedTransport.ResponseHeaderTimeout = 60 * time.Second // Wait up to 60s for response headers
			clonedTransport.IdleConnTimeout = 90 * time.Second
			return clonedTransport
		}

		return rt
	}
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

	logger.Step(1, "Stopping test cluster tunnel and closing SSH client")
	var errs []error

	// Step 1: Stop test cluster tunnel and close test cluster SSH client
	if resources.TunnelInfo != nil && resources.TunnelInfo.StopFunc != nil {
		if err := resources.TunnelInfo.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop test cluster SSH tunnel: %w", err))
			logger.Error("Failed to stop test cluster SSH tunnel: %v", err)
		} else {
			logger.Success("Test cluster SSH tunnel stopped")
		}
	}

	if resources.SSHClient != nil {
		if err := resources.SSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close test cluster SSH client: %w", err))
			logger.Error("Failed to close test cluster SSH client: %v", err)
		} else {
			logger.Success("Test cluster SSH client closed")
		}
	}

	logger.Step(2, "Closing setup SSH client")
	// Step 2: Close setup SSH client
	if resources.SetupSSHClient != nil {
		if err := resources.SetupSSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close setup SSH client: %w", err))
			logger.Error("Failed to close setup SSH client: %v", err)
		} else {
			logger.Success("Setup SSH client closed")
		}
	}

	logger.Step(3, "Re-establishing base cluster tunnel for VM cleanup")
	// Step 3: Re-establish base cluster tunnel if needed for VM cleanup
	// We need API access to remove VMs, so we need the tunnel
	var baseTunnel *ssh.TunnelInfo
	var cleanupKubeconfig *rest.Config
	if resources.BaseClusterClient != nil && resources.VMResources != nil {
		// Re-establish tunnel if it was stopped (BaseTunnelInfo is nil)
		if resources.BaseTunnelInfo == nil {
			logger.Progress("Re-establishing base cluster tunnel...")
			var tunnelErr error
			baseTunnel, tunnelErr = ssh.EstablishSSHTunnel(context.Background(), resources.BaseClusterClient, "6445")
			if tunnelErr != nil {
				errs = append(errs, fmt.Errorf("failed to re-establish base cluster tunnel for VM cleanup: %w", tunnelErr))
				logger.Error("Failed to re-establish base cluster tunnel: %v", tunnelErr)
			} else {
				logger.Success("Base cluster tunnel re-established on local port: %d", baseTunnel.LocalPort)
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
			logger.Success("Base cluster tunnel already exists")
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
					logger.Step(4, "Removing setup VM %s", resources.VMResources.SetupVMName)
					if removeErr := RemoveVM(ctx, virtClient, namespace, resources.VMResources.SetupVMName); removeErr != nil {
						errs = append(errs, fmt.Errorf("failed to remove setup VM %s: %w", resources.VMResources.SetupVMName, removeErr))
						logger.Error("Failed to remove setup VM %s: %v", resources.VMResources.SetupVMName, removeErr)
					} else {
						logger.Success("Setup VM %s removed", resources.VMResources.SetupVMName)
					}
				}

				// Step 5: Remove test cluster VMs if cleanup is enabled
				if config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True" {
					logger.Step(5, "Removing test cluster VMs (TEST_CLUSTER_CLEANUP is enabled)")
					if resources.VMResources != nil && len(resources.VMResources.VMNames) > 0 {
						logger.Progress("Removing %d VMs: %v", len(resources.VMResources.VMNames), resources.VMResources.VMNames)
					}
					if removeErr := RemoveAllVMs(ctx, resources.VMResources); removeErr != nil {
						errs = append(errs, fmt.Errorf("failed to remove test cluster VMs: %w", removeErr))
						logger.Error("Failed to remove test cluster VMs: %v", removeErr)
					} else {
						logger.Success("Test cluster VMs removed")
					}
				} else {
					logger.Skip("Skipping test cluster VM removal (TEST_CLUSTER_CLEANUP is not enabled)")
				}
			} else {
				errs = append(errs, fmt.Errorf("failed to create virtualization client for cleanup: %w", virtErr))
				logger.Error("Failed to create virtualization client for cleanup: %v", virtErr)
			}
		} else {
			logger.Warn("Cannot remove VMs - no valid kubeconfig for cleanup")
		}
	} else {
		if resources.VMResources == nil {
			logger.Skip("Skipping VM cleanup (no VM resources to clean up)")
		} else {
			logger.Warn("Cannot remove VMs - base cluster client not available")
		}
	}

	logger.Step(6, "Stopping base cluster tunnel and closing SSH client")
	// Step 6: Stop base cluster tunnel and close base cluster SSH client
	if baseTunnel != nil && baseTunnel.StopFunc != nil {
		if err := baseTunnel.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop base cluster SSH tunnel: %w", err))
			logger.Error("Failed to stop base cluster SSH tunnel: %v", err)
		} else {
			logger.Success("Base cluster SSH tunnel stopped")
		}
	}

	if resources.BaseClusterClient != nil {
		if err := resources.BaseClusterClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close base cluster SSH client: %w", err))
			logger.Error("Failed to close base cluster SSH client: %v", err)
		} else {
			logger.Success("Base cluster SSH client closed")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	return nil
}

// CheckClusterHealthOptions defines options for CheckClusterHealth
type CheckClusterHealthOptions struct {
	// CheckBootstrapSecrets enables checking for bootstrap secrets.
	// Default is true. Set to false for existing clusters that don't need bootstrap secrets.
	CheckBootstrapSecrets bool
}

// DefaultCheckClusterHealthOptions returns default options with all checks enabled
func DefaultCheckClusterHealthOptions() CheckClusterHealthOptions {
	return CheckClusterHealthOptions{
		CheckBootstrapSecrets: true,
	}
}

// CheckClusterHealth checks if the deckhouse deployment has 1 pod running with 2/2 containers ready
// in the d8-system namespace, optionally verifies that bootstrap secrets are available, and ensures webhook-handler pods are ready.
// This function is widely used to check cluster health after certain steps.
// It polls until the deployment is ready or the context times out.
func CheckClusterHealth(ctx context.Context, kubeconfig *rest.Config, opts ...CheckClusterHealthOptions) error {
	// Use default options if none provided
	options := DefaultCheckClusterHealthOptions()
	if len(opts) > 0 {
		options = opts[0]
	}
	namespace := "d8-system"
	deploymentName := "deckhouse"

	// Get clientset for checking deployment, with retry for transient network errors
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Wait for deployment to have 1 ready replica
	// Poll every 5 seconds until ready or context times out
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var deployment *appsv1.Deployment
	for {
		// Get the deployment
		var err error
		deployment, err = clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, deploymentName, err)
		}

		// Check if deployment has 1 ready replica (1 pod)
		if deployment.Status.ReadyReplicas >= 1 {
			break // Deployment is ready, continue with pod checks
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deployment %s/%s to have ready replicas (current: %d, expected: 1): %w", namespace, deploymentName, deployment.Status.ReadyReplicas, ctx.Err())
		case <-ticker.C:
			// Continue polling
		}
	}

	// Check that bootstrap secrets are available (optional)
	if options.CheckBootstrapSecrets {
		secretNamespace := "d8-cloud-instance-manager"
		if err := checkBootstrapSecrets(ctx, kubeconfig, secretNamespace); err != nil {
			return fmt.Errorf("bootstrap secrets not ready: %w", err)
		}
	}

	// Get pods for the deployment using the deployment's selector
	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pods for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check that we have exactly 1 pod
	if len(pods.Items) != 1 {
		return fmt.Errorf("expected 1 pod for deployment %s/%s, found %d", namespace, deploymentName, len(pods.Items))
	}

	// Check the pod is running and has 2/2 containers ready
	pod := pods.Items[0]
	if pod.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("pod %s/%s is not running (phase: %s)", namespace, pod.Name, pod.Status.Phase)
	}

	// Verify the pod has exactly 2 containers
	if len(pod.Spec.Containers) != 2 {
		return fmt.Errorf("pod %s/%s has %d containers, expected 2", namespace, pod.Name, len(pod.Spec.Containers))
	}

	// Check all containers are ready
	allReady := len(pod.Status.ContainerStatuses) == len(pod.Spec.Containers)
	if allReady {
		for _, status := range pod.Status.ContainerStatuses {
			if !status.Ready {
				allReady = false
				break
			}
		}
	}
	if !allReady {
		return fmt.Errorf("pod %s/%s does not have all containers ready (expected 2/2 containers ready)", namespace, pod.Name)
	}

	// Check that webhook-handler deployment is ready in d8-system namespace
	if err := checkWebhookHandler(ctx, clientset, namespace); err != nil {
		return fmt.Errorf("webhook-handler not ready: %w", err)
	}

	return nil
}

// checkBootstrapSecrets verifies that both bootstrap secrets are available
func checkBootstrapSecrets(ctx context.Context, kubeconfig *rest.Config, namespace string) error {
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Check for worker bootstrap secret
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, "manual-bootstrap-for-worker", metav1.GetOptions{})
	if err != nil {
		// List available secrets for debugging
		secretList, listErr := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
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
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, "manual-bootstrap-for-master", metav1.GetOptions{})
	if err != nil {
		// List available secrets for debugging
		secretList, listErr := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
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

// WaitForWebhookHandler waits for the webhook-handler deployment to be ready and verifies that
// the deckhouse service has endpoints registered on port 4223.
// This should be called before attempting to create/update ModuleConfigs to avoid webhook connection refused errors.
func WaitForWebhookHandler(ctx context.Context, kubeconfig *rest.Config, timeout time.Duration) error {
	clientset, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	namespace := "d8-system"
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for webhook-handler to be ready after %v", timeout)
			}

			if err := checkWebhookHandler(ctx, clientset, namespace); err == nil {
				return nil
			}
		}
	}
}

// checkWebhookHandler verifies that the webhook-handler deployment has the desired number of ready replicas
// and that the deckhouse service has endpoints registered on port 4223
func checkWebhookHandler(ctx context.Context, clientset *k8s.Clientset, namespace string) error {
	// Get the webhook-handler deployment
	deploymentName := "webhook-handler"
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check if deployment has desired replicas ready
	if deployment.Spec.Replicas == nil {
		return fmt.Errorf("deployment %s/%s has nil replicas spec", namespace, deploymentName)
	}

	desiredReplicas := *deployment.Spec.Replicas
	readyReplicas := deployment.Status.ReadyReplicas

	if readyReplicas != desiredReplicas {
		return fmt.Errorf("deployment %s/%s has %d ready replicas, expected %d", namespace, deploymentName, readyReplicas, desiredReplicas)
	}

	if readyReplicas == 0 {
		return fmt.Errorf("deployment %s/%s has 0 ready replicas", namespace, deploymentName)
	}

	// Check that the deckhouse service has endpoints registered on port 4223
	// This ensures the webhook is actually accessible
	endpoints, err := clientset.CoreV1().Endpoints(namespace).Get(ctx, "deckhouse", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deckhouse service endpoints: %w", err)
	}

	// Check if there are any endpoints on port 4223 (webhook port)
	hasWebhookEndpoint := false
	for _, subset := range endpoints.Subsets {
		for _, port := range subset.Ports {
			if port.Port == 4223 {
				if len(subset.Addresses) > 0 {
					hasWebhookEndpoint = true
					break
				}
			}
		}
		if hasWebhookEndpoint {
			break
		}
	}

	if !hasWebhookEndpoint {
		return fmt.Errorf("deckhouse service has no endpoints registered on port 4223 (webhook port)")
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
		maxRetries := config.SSHRetryCount
		retryDelay := config.SSHRetryInitialDelay
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
				if retryDelay > config.SSHRetryMaxDelay {
					retryDelay = config.SSHRetryMaxDelay
				}
			}

			sshClient, lastErr = ssh.NewClientWithJumpHost(
				jumpHostUser, jumpHostHost, jumpHostKeyPath, // jump host
				opts.TargetUser, opts.TargetHost, targetKeyPath, // target
			)
			if lastErr == nil {
				break // Success
			}
			logger.Warn("SSH connection with jump host attempt %d/%d failed: %v", attempt+1, maxRetries, lastErr)
		}
		if lastErr != nil {
			return nil, fmt.Errorf("failed to create SSH client with jump host after %d attempts: %w", maxRetries, lastErr)
		}

		masterHost = opts.TargetHost
		masterUser = opts.TargetUser
	} else {
		// Direct connection (no jump host) with retry logic
		maxRetries := config.SSHRetryCount
		retryDelay := config.SSHRetryInitialDelay
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
				if retryDelay > config.SSHRetryMaxDelay {
					retryDelay = config.SSHRetryMaxDelay
				}
			}

			sshClient, lastErr = ssh.NewClient(opts.SSHUser, opts.SSHHost, opts.SSHKeyPath)
			if lastErr == nil {
				break // Success
			}
			logger.Warn("SSH connection attempt %d/%d failed: %v", attempt+1, maxRetries, lastErr)
		}
		if lastErr != nil {
			return nil, fmt.Errorf("failed to create SSH client after %d attempts: %w", maxRetries, lastErr)
		}

		masterHost = opts.SSHHost
		masterUser = opts.SSHUser
	}

	// Step 2: Establish SSH tunnel with port forwarding localPort:127.0.0.1:6445
	// Uses a dynamically allocated local port to support parallel test runs
	// Use context.Background() for the tunnel so it persists after the function returns
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

	// Step 4: Update kubeconfig to use the dynamically allocated tunnel port
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

	// Configure extended timeouts for tunnel-based connections
	configureExtendedTimeouts(kubeconfig)

	// Return resources with active tunnel
	// Note: The test will use Eventually to check cluster health with CheckClusterHealth
	return &TestClusterResources{
		SSHClient:      sshClient,
		Kubeconfig:     kubeconfig,
		KubeconfigPath: kubeconfigPath,
		TunnelInfo:     tunnelInfo,
	}, nil
}

// GenerateRandomSuffix generates a random alphanumeric suffix of specified length
func GenerateRandomSuffix(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// OutputEnvironmentVariables outputs environment variables to GinkgoWriter for debugging
func OutputEnvironmentVariables() {
	GinkgoWriter.Printf("    📋 Environment variables (without default values):\n")

	// Helper function to mask sensitive values
	maskValue := func(value string, mask bool) string {
		if mask && len(value) > 5 {
			return value[:5] + "***"
		}
		return value
	}

	// DKP_LICENSE_KEY - mask first 5 characters
	if config.DKPLicenseKey != "" {
		GinkgoWriter.Printf("      DKP_LICENSE_KEY: %s\n", maskValue(config.DKPLicenseKey, true))
	}

	// REGISTRY_DOCKER_CFG - mask first 5 characters
	if config.RegistryDockerCfg != "" {
		GinkgoWriter.Printf("      REGISTRY_DOCKER_CFG: %s\n", maskValue(config.RegistryDockerCfg, true))
	}

	// TEST_CLUSTER_CREATE_MODE - no masking, with explanation
	if config.TestClusterCreateMode != "" {
		modeExplanation := ""
		switch config.TestClusterCreateMode {
		case config.ClusterCreateModeAlwaysUseExisting:
			modeExplanation = " (will use existing cluster with lock)"
		case config.ClusterCreateModeAlwaysCreateNew:
			modeExplanation = " (will create new VMs for test cluster)"
		case config.ClusterCreateModeCommander:
			modeExplanation = " (will use Deckhouse Commander to create/use cluster)"
		}
		GinkgoWriter.Printf("      TEST_CLUSTER_CREATE_MODE: %s%s\n", config.TestClusterCreateMode, modeExplanation)
	}

	// Commander-specific environment variables (only shown when commander mode is used)
	if config.TestClusterCreateMode == config.ClusterCreateModeCommander {
		GinkgoWriter.Printf("    📋 Deckhouse Commander configuration:\n")
		if config.CommanderURL != "" {
			GinkgoWriter.Printf("      COMMANDER_URL: %s\n", config.CommanderURL)
		}
		if config.CommanderToken != "" {
			GinkgoWriter.Printf("      COMMANDER_TOKEN: %s\n", maskValue(config.CommanderToken, true))
		}
		if config.CommanderClusterName != "" {
			GinkgoWriter.Printf("      COMMANDER_CLUSTER_NAME: %s\n", config.CommanderClusterName)
		}
		if config.CommanderTemplateName != "" {
			GinkgoWriter.Printf("      COMMANDER_TEMPLATE_NAME: %s\n", config.CommanderTemplateName)
		}
		if config.CommanderTemplateVersion != "" {
			GinkgoWriter.Printf("      COMMANDER_TEMPLATE_VERSION: %s\n", config.CommanderTemplateVersion)
		}
		if config.CommanderCreateIfNotExists != "" {
			GinkgoWriter.Printf("      COMMANDER_CREATE_IF_NOT_EXISTS: %s\n", config.CommanderCreateIfNotExists)
		}
		if config.CommanderWaitTimeout != "" {
			GinkgoWriter.Printf("      COMMANDER_WAIT_TIMEOUT: %s\n", config.CommanderWaitTimeout)
		}
		if config.CommanderInsecureSkipTLSVerify == "true" {
			GinkgoWriter.Printf("      COMMANDER_INSECURE_SKIP_TLS_VERIFY: true (⚠️ TLS verification disabled)\n")
		}
		if config.CommanderCACert != "" {
			GinkgoWriter.Printf("      COMMANDER_CA_CERT: %s\n", config.CommanderCACert)
		}
		if config.CommanderAuthMethod != "" {
			GinkgoWriter.Printf("      COMMANDER_AUTH_METHOD: %s\n", config.CommanderAuthMethod)
		} else {
			GinkgoWriter.Printf("      COMMANDER_AUTH_METHOD: bearer (default)\n")
		}
		if config.CommanderAuthUser != "" {
			GinkgoWriter.Printf("      COMMANDER_AUTH_USER: %s\n", config.CommanderAuthUser)
		}
		if config.CommanderAPIPrefix != "" {
			GinkgoWriter.Printf("      COMMANDER_API_PREFIX: %s\n", config.CommanderAPIPrefix)
		} else {
			GinkgoWriter.Printf("      COMMANDER_API_PREFIX: %s (default)\n", config.CommanderAPIPrefixDefaultValue)
		}
	}

	// TEST_CLUSTER_CLEANUP - no masking
	if config.TestClusterCleanup != "" {
		GinkgoWriter.Printf("      TEST_CLUSTER_CLEANUP: %s\n", config.TestClusterCleanup)
	}

	// TEST_CLUSTER_NAMESPACE - no masking
	if config.TestClusterNamespace != "" {
		GinkgoWriter.Printf("      TEST_CLUSTER_NAMESPACE: %s\n", config.TestClusterNamespace)
	}

	// TEST_CLUSTER_STORAGE_CLASS - no masking
	if config.TestClusterStorageClass != "" {
		GinkgoWriter.Printf("      TEST_CLUSTER_STORAGE_CLASS: %s\n", config.TestClusterStorageClass)
	}

	// SSH_HOST - no masking
	if config.SSHHost != "" {
		GinkgoWriter.Printf("      SSH_HOST: %s\n", config.SSHHost)
	}

	// SSH_USER - no masking
	if config.SSHUser != "" {
		GinkgoWriter.Printf("      SSH_USER: %s\n", config.SSHUser)
	}

	// SSH_PRIVATE_KEY - show path (not content, could be base64)
	if config.SSHPrivateKey != "" {
		if strings.Contains(config.SSHPrivateKey, "/") || strings.HasPrefix(config.SSHPrivateKey, "~") {
			GinkgoWriter.Printf("      SSH_PRIVATE_KEY: %s\n", config.SSHPrivateKey)
		} else {
			GinkgoWriter.Printf("      SSH_PRIVATE_KEY: <base64 content>\n")
		}
	} else {
		GinkgoWriter.Printf("      SSH_PRIVATE_KEY: %s (default)\n", config.SSHPrivateKeyDefaultValue)
	}

	// SSH_PUBLIC_KEY - show path or indicate inline content
	if config.SSHPublicKey != "" {
		if strings.Contains(config.SSHPublicKey, "/") || strings.HasPrefix(config.SSHPublicKey, "~") {
			GinkgoWriter.Printf("      SSH_PUBLIC_KEY: %s\n", config.SSHPublicKey)
		} else {
			GinkgoWriter.Printf("      SSH_PUBLIC_KEY: <inline content>\n")
		}
	} else {
		GinkgoWriter.Printf("      SSH_PUBLIC_KEY: %s (default)\n", config.SSHPublicKeyDefaultValue)
	}

	// SSH_JUMP_HOST - no masking (optional, for existing cluster mode)
	if config.SSHJumpHost != "" {
		GinkgoWriter.Printf("      SSH_JUMP_HOST: %s\n", config.SSHJumpHost)
	}

	// SSH_JUMP_USER - no masking (optional, for existing cluster mode)
	if config.SSHJumpUser != "" {
		GinkgoWriter.Printf("      SSH_JUMP_USER: %s\n", config.SSHJumpUser)
	}

	// SSH_JUMP_KEY_PATH - no masking (optional, for existing cluster mode)
	if config.SSHJumpKeyPath != "" {
		GinkgoWriter.Printf("      SSH_JUMP_KEY_PATH: %s\n", config.SSHJumpKeyPath)
	}

	// SSH_PASSPHRASE - no masking (optional, may be empty)
	if config.SSHPassphrase != "" {
		GinkgoWriter.Printf("      SSH_PASSPHRASE: <set>\n")
	}

	// LOG_LEVEL - no masking
	if config.LogLevel != "" {
		GinkgoWriter.Printf("      LOG_LEVEL: %s\n", config.LogLevel)
	}

	// KUBE_CONFIG_PATH - no masking (optional, may be empty)
	if config.KubeConfigPath != "" {
		GinkgoWriter.Printf("      KUBE_CONFIG_PATH: %s\n", config.KubeConfigPath)
	}

	// Stress test environment variables (only show if set, otherwise defaults will be used)
	GinkgoWriter.Printf("    📋 Stress test configuration (from env vars or defaults):\n")
	if config.StressTestPVCSize != "" {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE: %s\n", config.StressTestPVCSize)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE: %s (default)\n", config.StressTestPVCSizeDefaultValue)
	}
	if config.StressTestPodsCount != "" {
		GinkgoWriter.Printf("      STRESS_TEST_PODS_COUNT: %s\n", config.StressTestPodsCount)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_PODS_COUNT: %s (default)\n", config.StressTestPodsCountDefaultValue)
	}
	if config.StressTestPVCSizeAfterResize != "" {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE_AFTER_RESIZE: %s\n", config.StressTestPVCSizeAfterResize)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE_AFTER_RESIZE: %s (default)\n", config.StressTestPVCSizeAfterResizeDefaultValue)
	}
	if config.StressTestPVCSizeAfterResizeStage2 != "" {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE_AFTER_RESIZE_STAGE2: %s\n", config.StressTestPVCSizeAfterResizeStage2)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_PVC_SIZE_AFTER_RESIZE_STAGE2: %s (default)\n", config.StressTestPVCSizeAfterResizeStage2DefaultValue)
	}
	if config.StressTestSnapshotsPerPVC != "" {
		GinkgoWriter.Printf("      STRESS_TEST_SNAPSHOTS_PER_PVC: %s\n", config.StressTestSnapshotsPerPVC)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_SNAPSHOTS_PER_PVC: %s (default)\n", config.StressTestSnapshotsPerPVCDefaultValue)
	}
	if config.StressTestMaxAttempts != "" {
		GinkgoWriter.Printf("      STRESS_TEST_MAX_ATTEMPTS: %s\n", config.StressTestMaxAttempts)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_MAX_ATTEMPTS: %s (default)\n", config.StressTestMaxAttemptsDefaultValue)
	}
	if config.StressTestInterval != "" {
		GinkgoWriter.Printf("      STRESS_TEST_INTERVAL: %ss\n", config.StressTestInterval)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_INTERVAL: %ss (default)\n", config.StressTestIntervalDefaultValue)
	}
	if config.StressTestCleanup != "" {
		GinkgoWriter.Printf("      STRESS_TEST_CLEANUP: %s\n", config.StressTestCleanup)
	} else {
		GinkgoWriter.Printf("      STRESS_TEST_CLEANUP: %s (default)\n", config.StressTestCleanupDefaultValue)
	}
}

// CreateOrConnectToTestCluster creates a new test cluster or connects to an existing one based on configuration.
// Returns the TestClusterResources which should be stored for later use in tests and cleanup.
// Supports three modes:
// - alwaysUseExisting: Connect to an existing cluster
// - alwaysCreateNew: Create a new cluster using VMs
// - commander: Use Deckhouse Commander to create or use a cluster
func CreateOrConnectToTestCluster() *TestClusterResources {
	var testClusterResources *TestClusterResources

	switch config.TestClusterCreateMode {
	case config.ClusterCreateModeAlwaysUseExisting:
		// Use existing cluster mode
		By("Connecting to existing cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCreationTimeout)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Connecting to existing cluster (mode: %s)\n", config.TestClusterCreateMode)
			var err error
			testClusterResources, err = UseExistingCluster(ctx)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Failed to connect to existing cluster: %v\n", err)
				Expect(err).NotTo(HaveOccurred(), "Should connect to existing cluster successfully")
			}
			GinkgoWriter.Printf("    ✅ Connected to existing cluster successfully (cluster lock acquired)\n")
		})

	case config.ClusterCreateModeCommander:
		// Use Deckhouse Commander mode
		By("Connecting to cluster via Deckhouse Commander", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCreationTimeout)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Using Deckhouse Commander (mode: %s)\n", config.TestClusterCreateMode)
			GinkgoWriter.Printf("      Commander URL: %s\n", config.CommanderURL)
			GinkgoWriter.Printf("      Cluster name: %s\n", config.CommanderClusterName)
			if config.CommanderCreateIfNotExists == "true" {
				GinkgoWriter.Printf("      Create if not exists: true (template: %s)\n", config.CommanderTemplateName)
			}

			var err error
			cmdResources, err := UseCommanderCluster(ctx)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Failed to use Commander cluster: %v\n", err)
				Expect(err).NotTo(HaveOccurred(), "Should connect to Commander cluster successfully")
			}

			// Store commander resources for cleanup
			SetCommanderResources(cmdResources)

			// Extract TestClusterResources from CommanderClusterResources
			testClusterResources = cmdResources.TestClusterResources

			if cmdResources.CreatedByUs {
				GinkgoWriter.Printf("    ✅ Created new cluster '%s' via Commander successfully\n", cmdResources.ClusterName)
			} else {
				GinkgoWriter.Printf("    ✅ Connected to existing cluster '%s' via Commander successfully\n", cmdResources.ClusterName)
			}
		})

	case config.ClusterCreateModeAlwaysCreateNew:
		fallthrough
	default:
		// Create new cluster mode (default)
		By("Creating test cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCreationTimeout)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Creating test cluster (mode: %s)\n", config.TestClusterCreateMode)
			var err error
			testClusterResources, err = CreateTestCluster(ctx, config.YAMLConfigFilename)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Failed to create test cluster: %v\n", err)
				Expect(err).NotTo(HaveOccurred(), "Test cluster should be created successfully")
			}
			GinkgoWriter.Printf("    ✅ Test cluster created successfully\n")
		})

		By("Waiting for test cluster to become ready", func() {
			// Create a new context with ModuleDeployTimeout for module readiness
			ctx, cancel := context.WithTimeout(context.Background(), config.ModuleDeployTimeout)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Waiting for all modules to be ready in test cluster (timeout: %v)...\n", config.ModuleDeployTimeout)
			err := WaitForTestClusterReady(ctx, testClusterResources)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Failed to wait for test cluster to be ready: %v\n", err)
				Expect(err).NotTo(HaveOccurred(), "Test cluster should become ready")
			}
			GinkgoWriter.Printf("    ✅ Test cluster is ready (all modules are Ready)\n")
		})
	}

	return testClusterResources
}

// CleanupTestClusterResources cleans up test cluster resources based on the mode used.
// If testPassed is true, stress test namespaces will be cleaned up before closing connections.
func CleanupTestClusterResources(testClusterResources *TestClusterResources, testPassed ...bool) {
	if testClusterResources == nil {
		return
	}

	// Determine if test passed (default to checking Ginkgo's CurrentSpecReport if not provided)
	passed := false
	if len(testPassed) > 0 {
		passed = testPassed[0]
	} else {
		// Use Ginkgo's CurrentSpecReport to check if test passed
		report := CurrentSpecReport()
		passed = !report.Failed()
	}

	if passed {
		GinkgoWriter.Printf("    ℹ️ Test passed - stress namespaces will be cleaned up\n")
	} else {
		GinkgoWriter.Printf("    ℹ️ Test failed - skipping stress namespace cleanup for debugging\n")
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCleanupTimeout)
	defer cancel()

	// Clean up stress namespaces if test passed (while we still have cluster access)
	if passed && testClusterResources.Kubeconfig != nil {
		GinkgoWriter.Printf("    ▶️ Cleaning up stress test namespaces...\n")
		if err := testkit.CleanupStressNamespaces(ctx, testClusterResources.Kubeconfig); err != nil {
			GinkgoWriter.Printf("    ⚠️  Warning: Failed to cleanup stress namespaces: %v\n", err)
		} else {
			GinkgoWriter.Printf("    ✅ Stress namespaces cleanup completed\n")
		}
	}

	switch config.TestClusterCreateMode {
	case config.ClusterCreateModeAlwaysUseExisting:
		// For existing cluster mode, just release the lock and close connections
		GinkgoWriter.Printf("    ▶️ Releasing cluster lock and closing connections...\n")
		err := CleanupExistingCluster(ctx, testClusterResources)
		if err != nil {
			GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
		} else {
			GinkgoWriter.Printf("    ✅ Cluster lock released and connections closed successfully\n")
		}

	case config.ClusterCreateModeCommander:
		// For Commander mode, cleanup using Commander-specific cleanup
		cmdResources := GetCommanderResources()
		if cmdResources != nil {
			cleanupDisabled := config.TestClusterCleanup == "false" || config.TestClusterCleanup == "False"
			if cmdResources.CreatedByUs && !cleanupDisabled {
				GinkgoWriter.Printf("    ▶️ Cleaning up Commander cluster '%s' (cluster was created by us, will be deleted)...\n", cmdResources.ClusterName)
			} else if cmdResources.CreatedByUs && cleanupDisabled {
				GinkgoWriter.Printf("    ▶️ Releasing Commander cluster lock (TEST_CLUSTER_CLEANUP=false, cluster will NOT be deleted)...\n")
			} else {
				GinkgoWriter.Printf("    ▶️ Releasing Commander cluster lock and closing connections...\n")
			}
			err := CleanupCommanderCluster(ctx, cmdResources)
			if err != nil {
				GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
			} else {
				GinkgoWriter.Printf("    ✅ Commander cluster resources cleaned up successfully\n")
			}
			ClearCommanderResources() // Clear the reference
		} else {
			// Fallback to existing cluster cleanup if cmdResources is nil
			GinkgoWriter.Printf("    ▶️ Releasing cluster lock and closing connections...\n")
			err := CleanupExistingCluster(ctx, testClusterResources)
			if err != nil {
				GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
			} else {
				GinkgoWriter.Printf("    ✅ Cluster lock released and connections closed successfully\n")
			}
		}

	case config.ClusterCreateModeAlwaysCreateNew:
		fallthrough
	default:
		// For new cluster mode, cleanup VMs based on TEST_CLUSTER_CLEANUP setting
		// Note: Bootstrap node (setup VM) is always removed.
		// Test cluster VMs (masters and workers) are only removed if TEST_CLUSTER_CLEANUP='true' or 'True'
		cleanupEnabled := config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True"
		if cleanupEnabled {
			GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources (TEST_CLUSTER_CLEANUP is enabled - all VMs will be removed)...\n")
		} else {
			GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources (TEST_CLUSTER_CLEANUP is not enabled - only bootstrap node will be removed)...\n")
		}
		err := CleanupTestCluster(ctx, testClusterResources)
		if err != nil {
			GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
		} else {
			GinkgoWriter.Printf("    ✅ Test cluster resources cleaned up successfully\n")
		}
	}
}
