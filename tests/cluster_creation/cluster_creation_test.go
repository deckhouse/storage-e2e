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

	"github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
)

var _ = Describe("Cluster Creation Test", Ordered, func() {
	var (
		yamlConfigFilename       string = "cluster_creation_test.yml"
		baseClusterMasterIP      string = "172.17.1.67"
		baseClusterUser          string = "tfadm"
		baseClusterSSHPrivateKey string = "~/.ssh/id_rsa"

		err               error
		sshclient         ssh.SSHClient
		kubeconfig        *rest.Config
		kubeconfigPath    string
		tunnelinfo        *ssh.TunnelInfo
		clusterDefinition *config.ClusterDefinition
		module            *deckhouse.Module
	)

	BeforeAll(func() {
		var err error

		// Stage 1: LoadConfig - verifies and parses the config from yaml file
		By("LoadConfig: Loading and verifying cluster configuration from YAML", func() {
			GinkgoWriter.Printf("    ▶️ Loading cluster configuration from: %s\n", yamlConfigFilename)
			clusterDefinition, err = cluster.LoadClusterConfig(yamlConfigFilename)
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

			// Note: kubeconfig and kubeconfigPath are just config/file paths, no cleanup needed
			// The kubeconfig file is stored in temp/ directory and can be kept for debugging
		})

	}) // BeforeAll

	_ = clusterDefinition // TODO: use clusterDefinition

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
			kubeconfig, kubeconfigPath, err = cluster.GetKubeconfig(ctx, baseClusterMasterIP, baseClusterUser, baseClusterSSHPrivateKey, sshclient)
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
			Expect(tunnelinfo.LocalPort).To(BeNumerically(">=", 1024))
			GinkgoWriter.Printf("    ✅ SSH tunnel established on local port: %d\n", tunnelinfo.LocalPort)

			// Update kubeconfig if port differs from 6445
			if tunnelinfo.LocalPort != 6445 {
				By(fmt.Sprintf("Updating kubeconfig to use local port %d instead of 6445", tunnelinfo.LocalPort), func() {
					GinkgoWriter.Printf("    ▶️ Updating kubeconfig port from 6445 to %d\n", tunnelinfo.LocalPort)
					err = cluster.UpdateKubeconfigPort(kubeconfigPath, tunnelinfo.LocalPort)
					Expect(err).NotTo(HaveOccurred())
					GinkgoWriter.Printf("    ✅ Kubeconfig updated successfully\n")
				})
			}
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

}) // Describe: Cluster Creation
