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

package integration

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/rest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
)

var _ = Describe("Cluster Creation Step-by-Step Test", Ordered, func() {
	var (
		err                  error
		sshclient            ssh.SSHClient
		setupSSHClient       ssh.SSHClient
		kubeconfig           *rest.Config
		kubeconfigPath       string
		tunnelinfo           *ssh.TunnelInfo
		clusterDefinition    *config.ClusterDefinition
		module               *deckhouse.Module
		virtClient           *virtualization.Client
		vmResources          *cluster.VMResources
		bootstrapConfig      string
		testClusterResources *cluster.TestClusterResources
		sshKeyPath           string
		bootstrapKeyPath     string
	)

	BeforeAll(func() {
		By("Validating environment variables", func() {
			GinkgoWriter.Printf("    ▶️ Validating environment variables\n")
			err := config.ValidateEnvironment()
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Environment variables validated successfully\n")
		})

		By("Outputting environment variables without default values", func() {
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

			// TEST_CLUSTER_CREATE_MODE - no masking
			if config.TestClusterCreateMode != "" {
				GinkgoWriter.Printf("      TEST_CLUSTER_CREATE_MODE: %s\n", config.TestClusterCreateMode)
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

			// SSH_PASSPHRASE - no masking (optional, may be empty)
			if config.SSHPassphrase != "" {
				GinkgoWriter.Printf("      SSH_PASSPHRASE: <set>\n")
			}

			// KUBE_CONFIG_PATH - no masking (optional, may be empty)
			if config.KubeConfigPath != "" {
				GinkgoWriter.Printf("      KUBE_CONFIG_PATH: %s\n", config.KubeConfigPath)
			}
		})

		By("Getting SSH private key path", func() {
			GinkgoWriter.Printf("    ▶️ Getting SSH private key path\n")
			sshKeyPath, err = cluster.GetSSHPrivateKeyPath()
			Expect(err).NotTo(HaveOccurred(), "Failed to get SSH private key path")
			GinkgoWriter.Printf("    ✅ SSH private key path obtained successfully\n")
		})

		By("Getting bootstrap SSH key path (for VM connections)", func() {
			GinkgoWriter.Printf("    ▶️ Getting bootstrap SSH key path\n")
			bootstrapKeyPath, err = cluster.GetBootstrapSSHPrivateKeyPath()
			Expect(err).NotTo(HaveOccurred(), "Failed to get bootstrap SSH key path")
			GinkgoWriter.Printf("    ✅ Bootstrap SSH key path: %s\n", bootstrapKeyPath)
		})

		// Stage 1: LoadConfig - verifies and parses the config from yaml file
		By("LoadConfig: Loading and verifying cluster configuration from YAML", func() {
			yamlConfigFilename := config.YAMLConfigFilename
			GinkgoWriter.Printf("    ▶️ Loading cluster configuration from: %s\n", yamlConfigFilename)
			clusterDefinition, err = internalcluster.LoadClusterConfig(yamlConfigFilename)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Successfully loaded cluster configuration\n")
		})

		// DeferCleanup: Clean up all resources in reverse order of creation (it's a synonym for AfterAll)
		DeferCleanup(func() {
			// Step 0: Stop test cluster tunnel if it exists (it uses port 6445, blocking base cluster tunnel)
			if testClusterResources != nil && testClusterResources.TunnelInfo != nil && testClusterResources.TunnelInfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping test cluster SSH tunnel on local port %d...\n", testClusterResources.TunnelInfo.LocalPort)
				err := testClusterResources.TunnelInfo.StopFunc()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to stop test cluster SSH tunnel: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Test cluster SSH tunnel stopped successfully\n")
				}
			}

			// Step 0.5: Close test cluster SSH client
			if testClusterResources != nil && testClusterResources.SSHClient != nil {
				GinkgoWriter.Printf("    ▶️ Closing test cluster SSH client connection...\n")
				err := testClusterResources.SSHClient.Close()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to close test cluster SSH client: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Test cluster SSH client closed successfully\n")
				}
			}

			// Step 1: Re-establish SSH tunnel if needed for VM cleanup
			// we need it for VM cleanup
			if tunnelinfo == nil && sshclient != nil {
				GinkgoWriter.Printf("    ▶️ Re-establishing SSH tunnel for VM cleanup...\n")
				var tunnelErr error
				tunnelinfo, tunnelErr = ssh.EstablishSSHTunnel(context.Background(), sshclient, "6445")
				if tunnelErr != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to re-establish SSH tunnel: %v\n", tunnelErr)
					GinkgoWriter.Printf("    ⚠️  VM cleanup will be skipped due to missing tunnel\n")
				} else {
					GinkgoWriter.Printf("    ✅ SSH tunnel re-established on local port: %d\n", tunnelinfo.LocalPort)
				}
			}

			// Step 2: Close setup SSH client connection
			if setupSSHClient != nil {
				GinkgoWriter.Printf("    ▶️ Closing setup SSH client connection...\n")
				err := setupSSHClient.Close()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to close setup SSH client: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Setup SSH client closed successfully\n")
				}
			}

			// Step 3: Cleanup setup VM (needs API access via SSH tunnel, but not SSH client)
			vmRes := vmResources
			if vmRes != nil && vmRes.SetupVMName != "" {
				GinkgoWriter.Printf("    ▶️ Removing setup VM %s...\n", vmRes.SetupVMName)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				err := cluster.RemoveVM(ctx, vmRes.VirtClient, vmRes.Namespace, vmRes.SetupVMName)
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to remove setup VM: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Setup VM removed successfully\n")
				}
			}

			// Step 4: Cleanup test cluster VMs if enabled
			if config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True" {
				if vmRes != nil {
					GinkgoWriter.Printf("    ▶️ Cleaning up test cluster VMs...\n")
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					err := cluster.RemoveAllVMs(ctx, vmRes)
					if err != nil {
						GinkgoWriter.Printf("    ⚠️  Warning: Failed to cleanup test cluster VMs: %v\n", err)
					} else {
						GinkgoWriter.Printf("    ✅ Test cluster VMs cleaned up successfully\n")
					}
				}
			}

			// Step 5: Stop base cluster SSH tunnel (must be done before closing SSH client)
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping base cluster SSH tunnel on local port %d...\n", tunnelinfo.LocalPort)
				err := tunnelinfo.StopFunc()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to stop base cluster SSH tunnel: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Base cluster SSH tunnel stopped successfully\n")
				}
			}

			// Step 6: Close base cluster SSH client connection
			if sshclient != nil {
				GinkgoWriter.Printf("    ▶️ Closing base cluster SSH client connection...\n")
				err := sshclient.Close()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to close base cluster SSH client: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Base cluster SSH client closed successfully\n")
				}
			}
		})

	}) // BeforeAll

	// ---=== TEST BEGIN ===---

	// Step 1: Connect to base cluster (SSH connection, kubeconfig, and tunnel)
	It("should connect to the base cluster", func() {
		By(fmt.Sprintf("Connecting to base cluster %s@%s", config.SSHUser, config.SSHHost), func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Connecting to base cluster %s@%s\n", config.SSHUser, config.SSHHost)
			baseClusterResources, err := cluster.ConnectToCluster(ctx, cluster.ConnectClusterOptions{
				SSHUser:     config.SSHUser,
				SSHHost:     config.SSHHost,
				SSHKeyPath:  sshKeyPath,
				UseJumpHost: false,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to connect to base cluster")
			Expect(baseClusterResources).NotTo(BeNil())
			Expect(baseClusterResources.SSHClient).NotTo(BeNil())
			Expect(baseClusterResources.Kubeconfig).NotTo(BeNil())
			Expect(baseClusterResources.TunnelInfo).NotTo(BeNil())

			// Extract resources for backward compatibility with rest of the test
			sshclient = baseClusterResources.SSHClient
			kubeconfig = baseClusterResources.Kubeconfig
			kubeconfigPath = baseClusterResources.KubeconfigPath
			tunnelinfo = baseClusterResources.TunnelInfo

			GinkgoWriter.Printf("    ✅ Base cluster connection established successfully\n")
			GinkgoWriter.Printf("    ✅ Kubeconfig saved to: %s\n", kubeconfigPath)
			GinkgoWriter.Printf("    ✅ SSH tunnel active on local port: %d\n", tunnelinfo.LocalPort)
		})
	})

	// Step 2: Verify virtualization module is Ready in base cluster before creating VMs
	It("should make sure that virtualization module is Ready", func() {
		By("Checking if virtualization module is Ready", func() {
			GinkgoWriter.Printf("    ▶️ Getting module with timeout\n")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			module, err = deckhouse.GetModule(ctx, kubeconfig, "virtualization")
			Expect(err).NotTo(HaveOccurred())
			Expect(module).NotTo(BeNil())
			Expect(module.Status.Phase).To(Equal("Ready"), "Module status phase should be Ready")
			GinkgoWriter.Printf("    ✅ Module %s retrieved successfully with status: %s\n", module.Name, module.Status.Phase)
		})
	})

	// Step 3: Create test namespace if it doesn't exist
	It("should ensure test namespace exists", func() {
		By("Checking and creating test namespace if needed", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			namespace := config.TestClusterNamespace
			GinkgoWriter.Printf("    ▶️ Ensuring namespace %s exists\n", namespace)

			ns, err := kubernetes.CreateNamespaceIfNotExists(ctx, kubeconfig, namespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")
			Expect(ns).NotTo(BeNil())
			GinkgoWriter.Printf("    ✅ Namespace %s is ready\n", namespace)
		})
	})

	// Step 4: Create virtual machines and wait for them to become Running
	It("should create virtual machines from cluster definition", func() {
		By("Creating virtual machines", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
			defer cancel()

			// Create virtualization client
			GinkgoWriter.Printf("    ▶️ Creating virtualization client\n")
			virtClient, err = virtualization.NewClient(ctx, kubeconfig)
			Expect(err).NotTo(HaveOccurred())
			Expect(virtClient).NotTo(BeNil())
			GinkgoWriter.Printf("    ✅ Virtualization client initialized successfully\n")

			namespace := config.TestClusterNamespace
			GinkgoWriter.Printf("    ▶️ Creating VMs in namespace: %s\n", namespace)

			// Create virtual machines
			var vmNames []string
			vmNames, vmResources, err = cluster.CreateVirtualMachines(ctx, virtClient, clusterDefinition)
			Expect(err).NotTo(HaveOccurred(), "Failed to create virtual machines")
			GinkgoWriter.Printf("    ✅ Created %d virtual machines: %v\n", len(vmNames), vmNames)

			GinkgoWriter.Printf("    ▶️ Waiting for all %d VMs to become Running (total timeout: %v)\n", len(vmNames), config.VMsRunningTimeout)
			loggedRunning := make(map[string]bool)
			Eventually(func() (bool, error) {
				allRunning := true
				for _, vmName := range vmNames {
					vm, err := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
					if err != nil {
						return false, fmt.Errorf("failed to get VM %s: %w", vmName, err)
					}
					if vm.Status.Phase == v1alpha2.MachineRunning {
						if !loggedRunning[vmName] {
							GinkgoWriter.Printf("    ✅ VM %s is Running\n", vmName)
							loggedRunning[vmName] = true
						}
					} else {
						allRunning = false
					}
				}
				return allRunning, nil
			}).WithTimeout(config.VMsRunningTimeout).WithPolling(20*time.Second).Should(BeTrue(),
				"All VMs should become Running within %v", config.VMsRunningTimeout)

			GinkgoWriter.Printf("    ✅ All %d VMs are Running\n", len(vmNames))
		})
	})

	// Step 5: Gather VM information (IPs, etc.) while still connected to base cluster
	It("should gather VM information", func() {
		By("Gathering VM information", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			namespace := config.TestClusterNamespace

			GinkgoWriter.Printf("    ▶️ Gathering IP addresses and VM information for all VMs\n")
			var err error
			err = cluster.GatherVMInfo(ctx, virtClient, namespace, clusterDefinition, vmResources)
			Expect(err).NotTo(HaveOccurred(), "Failed to gather VM information")

			// Log all gathered IPs
			vmCount := 0
			for _, master := range clusterDefinition.Masters {
				if master.HostType == config.HostTypeVM && master.IPAddress != "" {
					GinkgoWriter.Printf("    ✅ VM %s has IP: %s\n", master.Hostname, master.IPAddress)
					vmCount++
				}
			}
			for _, worker := range clusterDefinition.Workers {
				if worker.HostType == config.HostTypeVM && worker.IPAddress != "" {
					GinkgoWriter.Printf("    ✅ VM %s has IP: %s\n", worker.Hostname, worker.IPAddress)
					vmCount++
				}
			}
			if clusterDefinition.Setup != nil && clusterDefinition.Setup.HostType == config.HostTypeVM && clusterDefinition.Setup.IPAddress != "" {
				GinkgoWriter.Printf("    ✅ VM %s has IP: %s\n", clusterDefinition.Setup.Hostname, clusterDefinition.Setup.IPAddress)
				vmCount++
			}

			GinkgoWriter.Printf("    ✅ Successfully gathered information for %d VMs\n", vmCount)
		})
	})

	// Step 6: Establish SSH connection to setup node through base cluster master (jump host)
	It("should establish SSH connection to setup node through base cluster master", func() {
		By("Obtaining SSH client to setup node through base cluster master", func() {
			// Note: We don't need to stop the base cluster tunnel here.
			// Jump host clients are just SSH connections and don't require port forwarding.
			// The base cluster tunnel can stay active for virtClient operations.

			setupNode, err := cluster.GetSetupNode(clusterDefinition)
			Expect(err).NotTo(HaveOccurred())

			// Get setup node IP address from cluster definition
			setupNodeIP := setupNode.IPAddress
			Expect(setupNodeIP).NotTo(BeEmpty(), "Setup node IP address should be set (gathered in Step 5)")

			// Create SSH client with jump host (base cluster master)
			GinkgoWriter.Printf("    ▶️ Creating SSH client to %s@%s through jump host %s@%s\n",
				config.VMSSHUser, setupNodeIP, config.SSHUser, config.SSHHost)
			setupSSHClient, err = ssh.NewClientWithJumpHost(
				config.SSHUser, config.SSHHost, sshKeyPath, // jump host
				config.VMSSHUser, setupNodeIP, sshKeyPath, // target host (user's key added via cloud-init)
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(setupSSHClient).NotTo(BeNil())
			GinkgoWriter.Printf("    ✅ SSH connection to setup node established successfully\n")
		})
	})

	// Step 6.5: Verify VM configuration (hostname, etc.)
	// NOTE: This step can potentially be removed if DVP correctly sets hostname from VM name
	It("should verify VM configuration on setup node", func() {
		By("Verifying VM configuration on setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Verifying VM configuration on setup node\n")
			err := cluster.VerifyVMConfig(ctx, setupSSHClient, "setup-node")
			if err != nil {
				GinkgoWriter.Printf("    ⚠️  Warning: VM configuration check failed: %v\n", err)
				// Continue anyway - this is a verification step
			} else {
				GinkgoWriter.Printf("    ✅ VM configuration verified on setup node\n")
			}
		})
	})

	// Step 7: Install Docker on setup node (required for DKP bootstrap)
	It("should ensure Docker is installed on the setup node", func() {
		By("Installing Docker on setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Installing Docker on setup node\n")
			err := cluster.InstallDocker(ctx, setupSSHClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to install Docker on setup node")
			GinkgoWriter.Printf("    ✅ Docker installed and running successfully on setup node\n")
		})
	})

	// Step 8: Prepare bootstrap configuration file from template with cluster-specific values
	It("should prepare bootstrap config for the setup node", func() {
		By("Preparing bootstrap config for the setup node", func() {
			GinkgoWriter.Printf("    ▶️ Preparing bootstrap config for the setup node\n")
			bootstrapConfig, err = cluster.PrepareBootstrapConfig(clusterDefinition)
			Expect(err).NotTo(HaveOccurred(), "Failed to prepare bootstrap config for the setup node")
			GinkgoWriter.Printf("    ✅ Bootstrap config prepared successfully at: %s\n", bootstrapConfig)
		})
	})

	// Step 8: Upload private key and config.yml to setup node for DKP bootstrap
	It("should upload bootstrap files to the setup node", func() {
		By("Uploading private key and config.yml to setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Uploading bootstrap files to setup node\n")
			GinkgoWriter.Printf("    📁 Private key: %s -> /home/cloud/.ssh/id_rsa\n", bootstrapKeyPath)
			GinkgoWriter.Printf("    📁 Config file: %s -> /home/cloud/config.yml\n", bootstrapConfig)

			err = cluster.UploadBootstrapFiles(ctx, setupSSHClient, bootstrapKeyPath, bootstrapConfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to upload bootstrap files to setup node")
			GinkgoWriter.Printf("    ✅ Bootstrap files uploaded successfully\n")
		})
	})

	// Step 9: Bootstrap cluster from setup node to first master node
	It("should bootstrap cluster from setup node to first master", func() {
		By("Bootstrapping cluster from setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
			defer cancel()

			firstMasterHostname := clusterDefinition.Masters[0].Hostname
			firstMasterIP := clusterDefinition.Masters[0].IPAddress
			Expect(firstMasterIP).NotTo(BeEmpty(), "Master node %s IP address should be set (gathered in Step 5)", firstMasterHostname)

			GinkgoWriter.Printf("    ▶️ Bootstrapping cluster from setup node to master %s (%s)\n", firstMasterHostname, firstMasterIP)
			GinkgoWriter.Printf("    ⏱️  This may take up to 30 minutes...\n")

			err = cluster.BootstrapCluster(ctx, setupSSHClient, clusterDefinition, bootstrapConfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to bootstrap cluster")
			GinkgoWriter.Printf("    ✅ Cluster bootstrap completed successfully\n")
		})
	})

	// Step 10: Create NodeGroup for workers
	It("should create NodeGroup for workers", func() {
		By("Creating NodeGroup for workers", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			firstMasterHostname := clusterDefinition.Masters[0].Hostname
			masterIP := clusterDefinition.Masters[0].IPAddress
			Expect(masterIP).NotTo(BeEmpty(), "Master node %s IP address should be set (gathered in Step 5)", firstMasterHostname)

			// Connect to test cluster to get kubeconfig (needed for NodeGroup creation)
			// Note: We need to stop base cluster tunnel first as both use port 6445
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping base cluster SSH tunnel (port 6445 needed for test cluster tunnel)...\n")
				err := tunnelinfo.StopFunc()
				Expect(err).NotTo(HaveOccurred(), "Failed to stop base cluster SSH tunnel")
				tunnelinfo = nil
				GinkgoWriter.Printf("    ✅ Base cluster SSH tunnel stopped successfully\n")
			}

			GinkgoWriter.Printf("    ▶️ Connecting to test cluster master %s through jump host %s@%s\n", masterIP, config.SSHUser, config.SSHHost)
			testClusterResources, err = cluster.ConnectToCluster(ctx, cluster.ConnectClusterOptions{
				SSHUser:       config.SSHUser,
				SSHHost:       config.SSHHost,
				SSHKeyPath:    sshKeyPath,
				UseJumpHost:   true,
				TargetUser:    config.VMSSHUser,
				TargetHost:    masterIP,
				TargetKeyPath: sshKeyPath,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to establish connection to test cluster")
			Expect(testClusterResources).NotTo(BeNil())
			Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available")

			GinkgoWriter.Printf("    ✅ Connection established, kubeconfig saved to: %s\n", testClusterResources.KubeconfigPath)
			GinkgoWriter.Printf("    ✅ SSH tunnel active on local port: %d\n", testClusterResources.TunnelInfo.LocalPort)

			// Create NodeGroup for workers
			GinkgoWriter.Printf("    ▶️ Creating NodeGroup for workers\n")
			err = cluster.CreateStaticNodeGroup(ctx, testClusterResources.Kubeconfig, "worker")
			Expect(err).NotTo(HaveOccurred(), "Failed to create worker NodeGroup")
			GinkgoWriter.Printf("    ✅ NodeGroup for workers created successfully\n")
		})
	})

	// Step 11: Verify cluster is ready
	It("should verify cluster is ready", func() {
		By("Verifying cluster is ready", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			Expect(testClusterResources).NotTo(BeNil(), "Test cluster resources must be available from Step 10")
			Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available from Step 10")

			GinkgoWriter.Printf("    ▶️ Verifying cluster readiness\n")

			// Check cluster health with Eventually (wait up to 15 minutes for deckhouse to be ready and secrets to appear)
			GinkgoWriter.Printf("    ⏱️  Waiting for deckhouse deployment to become ready (1 pod with 2/2 containers ready) and bootstrap secrets to appear...\n")
			Eventually(func() error {
				return cluster.CheckClusterHealth(ctx, testClusterResources.Kubeconfig)
			}).WithTimeout(15*time.Minute).WithPolling(20*time.Second).Should(Succeed(),
				"Deckhouse deployment should have 1 pod with 2/2 containers ready and bootstrap secrets should be available within 15 minutes")

			GinkgoWriter.Printf("    ✅ Cluster is ready (deckhouse deployment: 1 pod with 2/2 containers ready, bootstrap secrets available)\n")
		})
	})

	// Step 12: Add nodes to the cluster
	It("should add all nodes to the cluster", func() {
		By("Adding nodes to the cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.NodesReadyTimeout)
			defer cancel()

			Expect(testClusterResources).NotTo(BeNil(), "Test cluster resources must be available from Step 10")
			Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available from Step 10")

			// Add all nodes to the cluster (skips first master, adds remaining masters and all workers)
			GinkgoWriter.Printf("    ▶️ Adding nodes to the cluster (remaining masters and all workers)\n")
			err = cluster.AddNodesToCluster(ctx, testClusterResources.Kubeconfig, clusterDefinition, config.SSHUser, config.SSHHost, sshKeyPath)
			Expect(err).NotTo(HaveOccurred(), "Failed to add nodes to cluster")
			GinkgoWriter.Printf("    ✅ All nodes added to cluster successfully\n")

			// Wait for all nodes to become Ready
			GinkgoWriter.Printf("    ⏱️  Waiting for all nodes to become Ready (timeout: %v)...\n", config.NodesReadyTimeout)
			Eventually(func() error {
				return cluster.WaitForAllNodesReady(ctx, testClusterResources.Kubeconfig, clusterDefinition, config.NodesReadyTimeout)
			}).WithTimeout(config.NodesReadyTimeout).WithPolling(10*time.Second).Should(Succeed(),
				"All expected nodes should be present and Ready within %v", config.NodesReadyTimeout)
			GinkgoWriter.Printf("    ✅ All nodes are Ready\n")
		})
	})

	// Step 13: Enable and configure modules from cluster definition in test cluster
	It("should enable and configure modules from cluster definition in test cluster", func() {
		By("Enabling and configuring modules in test cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			Expect(testClusterResources).NotTo(BeNil(), "Test cluster resources must be available")
			Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available")

			GinkgoWriter.Printf("    ▶️ Enabling and configuring modules from cluster definition in test cluster\n")
			// Use SSH client to run kubectl commands from within the cluster (webhook needs to be accessible from cluster network)
			err := cluster.EnableAndConfigureModules(ctx, testClusterResources.Kubeconfig, clusterDefinition, testClusterResources.SSHClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to enable and configure modules")
			GinkgoWriter.Printf("    ✅ Modules enabled and configured successfully in test cluster\n")
		})
	})

	// Step 14: Wait for all modules to be ready in test cluster
	It("should wait for all modules to be ready in test cluster", func() {
		By("Waiting for modules to be ready in test cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.ModuleDeployTimeout)
			defer cancel()

			Expect(testClusterResources).NotTo(BeNil(), "Test cluster resources must be available")
			Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available")

			GinkgoWriter.Printf("    ▶️ Waiting for modules to be ready in test cluster (timeout: %v)\n", config.ModuleDeployTimeout)
			err := cluster.WaitForModulesReady(ctx, testClusterResources.Kubeconfig, clusterDefinition, config.ModuleDeployTimeout)
			Expect(err).NotTo(HaveOccurred(), "Failed to wait for modules to be ready")
			GinkgoWriter.Printf("    ✅ All modules are ready in test cluster\n")
		})
	})
}) // Describe: Cluster Creation
