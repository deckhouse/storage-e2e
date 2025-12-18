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
	"os"
	"path/filepath"
	"strings"
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
		err               error
		sshclient         ssh.SSHClient
		setupSSHClient    ssh.SSHClient
		kubeconfig        *rest.Config
		kubeconfigPath    string
		tunnelinfo        *ssh.TunnelInfo
		clusterDefinition *config.ClusterDefinition
		module            *deckhouse.Module
		virtClient        *virtualization.Client
		vmResources       *cluster.VMResources
		bootstrapConfig   string
	)

	BeforeAll(func() {
		By("Validating environment variables", func() {
			GinkgoWriter.Printf("    ▶️ Validating environment variables\n")
			err := config.ValidateEnvironment()
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Environment variables validated successfully\n")
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
			// Step 1: Cleanup setup VM (needs API access via SSH tunnel)
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

			// Step 2: Cleanup test cluster VMs if enabled
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

			// Step 3: Close setup SSH client connection (no longer needed after VM cleanup)
			if setupSSHClient != nil {
				GinkgoWriter.Printf("    ▶️ Closing setup SSH client connection...\n")
				err := setupSSHClient.Close()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to close setup SSH client: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Setup SSH client closed successfully\n")
				}
			}

			// Step 4: Stop base cluster SSH tunnel (must be done before closing SSH client)
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping base cluster SSH tunnel on local port %d...\n", tunnelinfo.LocalPort)
				err := tunnelinfo.StopFunc()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to stop base cluster SSH tunnel: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ Base cluster SSH tunnel stopped successfully\n")
				}
			}

			// Step 5: Close base cluster SSH client connection
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

	// Step 3: Establish SSH connection to base cluster (reused for getting kubeconfig)
	It("should establish ssh connection to the base cluster", func() {
		By(fmt.Sprintf("Connecting to %s@%s using key %s", config.SSHUser, config.SSHHost, config.SSHKeyPath), func() {
			GinkgoWriter.Printf("    ▶️ Creating SSH client for %s@%s\n", config.SSHUser, config.SSHHost)
			sshclient, err = ssh.NewClient(config.SSHUser, config.SSHHost, config.SSHKeyPath)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ SSH connection established successfully\n")
		})
	})

	// Step 4: Getting kubeconfig from base cluster (reusing SSH connection to avoid double passphrase prompt)
	It("should get kubeconfig from the base cluster", func() {
		By("Retrieving kubeconfig from base cluster", func() {
			GinkgoWriter.Printf("    ▶️ Fetching kubeconfig from %s\n", config.SSHHost)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			kubeconfig, kubeconfigPath, err = internalcluster.GetKubeconfig(ctx, config.SSHHost, config.SSHUser, config.SSHKeyPath, sshclient)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Kubeconfig retrieved and saved to: %s\n", kubeconfigPath)
		})
	})

	// Step 5: Establish SSH tunnel with port forwarding to access Kubernetes API
	It("should establish ssh tunnel to the base cluster with port forwarding", func() {
		By("Setting up SSH tunnel with port forwarding", func() {
			GinkgoWriter.Printf("    ▶️ Establishing SSH tunnel to %s, forwarding port 6445\n", config.SSHHost)
			ctx := context.Background()
			tunnelinfo, err = ssh.EstablishSSHTunnel(ctx, sshclient, "6445")
			Expect(err).NotTo(HaveOccurred())
			Expect(tunnelinfo).NotTo(BeNil())
			Expect(tunnelinfo.LocalPort).To(Equal(6445), "Local port should be exactly 6445")
			GinkgoWriter.Printf("    ✅ SSH tunnel established on local port: %d\n", tunnelinfo.LocalPort)

		})
	})

	// Step 6: Verify virtualization module is Ready before creating VMs
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

	// Step 7: Create test namespace if it doesn't exist
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

	// Step 8: Create virtual machines and wait for them to become Running
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

	// Step 9: Establish SSH connection to setup node through base cluster master (jump host)
	It("should establish SSH connection to setup node through base cluster master", func() {
		By("Stopping current SSH tunnel to base cluster", func() {
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping SSH tunnel on local port %d...\n", tunnelinfo.LocalPort)
				err := tunnelinfo.StopFunc()
				Expect(err).NotTo(HaveOccurred())
				GinkgoWriter.Printf("    ✅ SSH tunnel stopped successfully\n")
				tunnelinfo = nil
			}
		})

		By("Obtaining SSH client to setup node through base cluster master", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			namespace := config.TestClusterNamespace
			setupNode, err := cluster.GetSetupNode(vmResources)
			Expect(err).NotTo(HaveOccurred())

			// Get setup node IP address
			setupNodeIP, err := cluster.GetVMIPAddress(ctx, virtClient, namespace, setupNode.Hostname)
			Expect(err).NotTo(HaveOccurred())
			Expect(setupNodeIP).NotTo(BeEmpty())

			// Create SSH client with jump host (base cluster master)
			GinkgoWriter.Printf("    ▶️ Creating SSH client to %s@%s through jump host %s@%s\n",
				config.VMSSHUser, setupNodeIP, config.SSHUser, config.SSHHost)
			setupSSHClient, err = ssh.NewClientWithJumpHost(
				config.SSHUser, config.SSHHost, config.SSHKeyPath, // jump host
				config.VMSSHUser, setupNodeIP, config.SSHKeyPath, // target host
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(setupSSHClient).NotTo(BeNil())
			GinkgoWriter.Printf("    ✅ SSH connection to setup node established successfully\n")
		})
	})

	// Step 10: Install Docker on setup node (required for DKP bootstrap)
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

	// Step 11: Prepare bootstrap configuration file from template with cluster-specific values
	It("should prepare bootstrap config for the setup node", func() {
		By("Preparing bootstrap config for the setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			namespace := config.TestClusterNamespace

			// Get IPs for all VMs (masters, workers, and setup node)
			var vmIPs []string
			allVMNames := append([]string{}, vmResources.VMNames...)
			allVMNames = append(allVMNames, vmResources.SetupVMName)

			GinkgoWriter.Printf("    ▶️ Getting IP addresses for all VMs\n")
			for _, vmName := range allVMNames {
				vmIP, err := cluster.GetVMIPAddress(ctx, virtClient, namespace, vmName)
				Expect(err).NotTo(HaveOccurred(), "Failed to get IP address for VM %s", vmName)
				Expect(vmIP).NotTo(BeEmpty(), "VM %s IP address should not be empty", vmName)
				vmIPs = append(vmIPs, vmIP)
				GinkgoWriter.Printf("    ✅ VM %s has IP: %s\n", vmName, vmIP)
			}

			firstMasterHostname := clusterDefinition.Masters[0].Hostname
			masterIP, err := cluster.GetVMIPAddress(ctx, virtClient, namespace, firstMasterHostname)
			Expect(err).NotTo(HaveOccurred(), "Failed to get IP address for master node %s", firstMasterHostname)
			Expect(masterIP).NotTo(BeEmpty(), "Master node %s IP address should not be empty", firstMasterHostname)
			GinkgoWriter.Printf("    ✅ Master node %s has IP: %s\n", firstMasterHostname, masterIP)

			GinkgoWriter.Printf("    ▶️ Preparing bootstrap config for the setup node\n")
			bootstrapConfig, err = cluster.PrepareBootstrapConfig(clusterDefinition, masterIP, vmIPs)
			Expect(err).NotTo(HaveOccurred(), "Failed to prepare bootstrap config for the setup node")
			GinkgoWriter.Printf("    ✅ Bootstrap config prepared successfully at: %s\n", bootstrapConfig)
		})
	})

	// Step 12: Upload private key and config.yml to setup node for DKP bootstrap
	It("should upload bootstrap files to the setup node", func() {
		By("Uploading private key and config.yml to setup node", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Expand SSH key path to handle ~
			keyPath := config.SSHKeyPath
			if strings.HasPrefix(keyPath, "~") {
				homeDir, err := os.UserHomeDir()
				Expect(err).NotTo(HaveOccurred())
				keyPath = filepath.Join(homeDir, strings.TrimPrefix(keyPath, "~/"))
			}

			GinkgoWriter.Printf("    ▶️ Uploading bootstrap files to setup node\n")
			GinkgoWriter.Printf("    📁 Private key: %s -> /home/cloud/.ssh/id_rsa\n", keyPath)
			GinkgoWriter.Printf("    📁 Config file: %s -> /home/cloud/config.yml\n", bootstrapConfig)

			err := cluster.UploadBootstrapFiles(ctx, setupSSHClient, keyPath, bootstrapConfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to upload bootstrap files to setup node")
			GinkgoWriter.Printf("    ✅ Bootstrap files uploaded successfully\n")
		})
	})
}) // Describe: Cluster Creation
