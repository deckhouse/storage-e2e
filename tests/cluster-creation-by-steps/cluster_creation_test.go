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
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
)

var _ = Describe("Cluster Creation Step-by-Step Test", Ordered, func() {
	var (
		yamlConfigFilename       string = "cluster_config.yml"
		baseClusterMasterIP      string = "10.0.0.181"
		baseClusterUser          string = "w-ansible"
		baseClusterSSHPrivateKey string = "~/.ssh/id_rsa"

		err               error
		sshclient         ssh.SSHClient
		kubeconfig        *rest.Config
		kubeconfigPath    string
		tunnelinfo        *ssh.TunnelInfo
		clusterDefinition *config.ClusterDefinition
		module            *deckhouse.Module
		virtClient        *virtualization.Client
		vmResources       *cluster.VMResources
	)

	BeforeAll(func() {
		var err error

		// Stage 1: LoadConfig - verifies and parses the config from yaml file
		By("LoadConfig: Loading and verifying cluster configuration from YAML", func() {
			GinkgoWriter.Printf("    ▶️ Loading cluster configuration from: %s\n", yamlConfigFilename)
			clusterDefinition, err = internalcluster.LoadClusterConfig(yamlConfigFilename)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Successfully loaded cluster configuration\n")
		})

		// DeferCleanup: Clean up all resources in reverse order of creation (it's a synonym for AfterAll)
		DeferCleanup(func() {
			// Step 1: Stop SSH tunnel (must be done before closing SSH client)
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				GinkgoWriter.Printf("    ▶️ Stopping SSH tunnel on local port %d...\n", tunnelinfo.LocalPort)
				err := tunnelinfo.StopFunc()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to stop SSH tunnel: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ SSH tunnel stopped successfully\n")
				}
			}

			// Step 2: Close SSH client connection
			if sshclient != nil {
				GinkgoWriter.Printf("    ▶️ Closing SSH client connection...\n")
				err := sshclient.Close()
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  Warning: Failed to close SSH client: %v\n", err)
				} else {
					GinkgoWriter.Printf("    ✅ SSH client closed successfully\n")
				}
			}

			// Step 3: Cleanup test cluster VMs if enabled
			// Note: vmResources is set in the test below, so we capture it in the closure
			vmRes := vmResources
			if config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True" {
				if vmRes != nil {
					GinkgoWriter.Printf("    ▶️ Cleaning up test cluster VMs...\n")
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					err := cluster.CleanupVMResources(ctx, vmRes)
					if err != nil {
						GinkgoWriter.Printf("    ⚠️  Warning: Failed to cleanup test cluster VMs: %v\n", err)
					} else {
						GinkgoWriter.Printf("    ✅ Test cluster VMs cleaned up successfully\n")
					}
				}
			}

			// Note: kubeconfig and kubeconfigPath are just config/file paths, no cleanup needed
			// The kubeconfig file is stored in temp/ directory and can be kept for debugging
		})

	}) // BeforeAll

	// Stage 2: Establish SSH connection to base cluster (reused for getting kubeconfig)
	It("should establish ssh connection to the base cluster", func() {
		By(fmt.Sprintf("Connecting to %s@%s using key %s", baseClusterUser, baseClusterMasterIP, baseClusterSSHPrivateKey), func() {
			GinkgoWriter.Printf("    ▶️ Creating SSH client for %s@%s\n", baseClusterUser, baseClusterMasterIP)
			sshclient, err = ssh.NewClient(baseClusterUser, baseClusterMasterIP, baseClusterSSHPrivateKey)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ SSH connection established successfully\n")
		})
	})

	// Stage 3: Getting kubeconfig from base cluster (reusing SSH connection to avoid double passphrase prompt)

	It("should get kubeconfig from the base cluster", func() {
		By("Retrieving kubeconfig from base cluster", func() {
			GinkgoWriter.Printf("    ▶️ Fetching kubeconfig from %s\n", baseClusterMasterIP)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			kubeconfig, kubeconfigPath, err = internalcluster.GetKubeconfig(ctx, baseClusterMasterIP, baseClusterUser, baseClusterSSHPrivateKey, sshclient)
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Kubeconfig retrieved and saved to: %s\n", kubeconfigPath)
		})
	})

	// Stage 4: Establish SSH tunnel with port forwarding

	It("should establish ssh tunnel to the base cluster with port forwarding", func() {
		By("Setting up SSH tunnel with port forwarding", func() {
			GinkgoWriter.Printf("    ▶️ Establishing SSH tunnel to %s, forwarding port 6445\n", baseClusterMasterIP)
			ctx := context.Background()
			tunnelinfo, err = ssh.EstablishSSHTunnel(ctx, sshclient, "6445")
			Expect(err).NotTo(HaveOccurred())
			Expect(tunnelinfo).NotTo(BeNil())
			Expect(tunnelinfo.LocalPort).To(Equal(6445), "Local port should be exactly 6445")
			GinkgoWriter.Printf("    ✅ SSH tunnel established on local port: %d\n", tunnelinfo.LocalPort)

		})
	})

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

	It("should create virtual machines from cluster definition", func() {
		By("Creating virtual machines", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Creating virtualization client\n")
			virtClient, err = virtualization.NewClient(ctx, kubeconfig)
			Expect(err).NotTo(HaveOccurred())
			Expect(virtClient).NotTo(BeNil())
			GinkgoWriter.Printf("    ✅ Virtualization client initialized successfully\n")

			namespace := clusterDefinition.DKPParameters.Namespace
			GinkgoWriter.Printf("    ▶️ Creating VMs in namespace: %s\n", namespace)

			// Create virtual machines
			var vmNames []string
			vmNames, vmResources, err = cluster.CreateVirtualMachines(ctx, virtClient, clusterDefinition)
			Expect(err).NotTo(HaveOccurred(), "Failed to create virtual machines")
			GinkgoWriter.Printf("    ✅ Created %d virtual machines: %v\n", len(vmNames), vmNames)

			// Wait for all VMs to become Running
			GinkgoWriter.Printf("    ▶️ Waiting for VMs to become Running (timeout: 10 minutes)\n")
			for _, vmName := range vmNames {
				Eventually(func() (v1alpha2.MachinePhase, error) {
					vm, err := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
					if err != nil {
						return "", err
					}
					return vm.Status.Phase, nil
				}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Equal(v1alpha2.MachineRunning),
					"VM %s should become Running within 10 minutes", vmName)
				GinkgoWriter.Printf("    ✅ VM %s is Running\n", vmName)
			}
			GinkgoWriter.Printf("    ✅ All VMs are Running\n")
		})
	})

}) // Describe: Cluster Creation
