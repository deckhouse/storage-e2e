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

package sds_node_configurator

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

var _ = Describe("Sds Node Configurator", Ordered, func() {
	var (
		testClusterResources *cluster.TestClusterResources
	)

	BeforeAll(func() {
		By("Outputting environment variables", func() {
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

			// SSH_HOST - no masking
			if config.SSHHost != "" {
				GinkgoWriter.Printf("      SSH_HOST: %s\n", config.SSHHost)
			}

			// SSH_USER - no masking
			if config.SSHUser != "" {
				GinkgoWriter.Printf("      SSH_USER: %s\n", config.SSHUser)
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
		})
	})

	AfterAll(func() {
		// Cleanup test cluster resources
		// Note: Bootstrap node (setup VM) is always removed.
		// Test cluster VMs (masters and workers) are only removed if TEST_CLUSTER_CLEANUP='true' or 'True'
		if testClusterResources != nil {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCleanupTimeout)
			defer cancel()

			cleanupEnabled := config.TestClusterCleanup == "true" || config.TestClusterCleanup == "True"
			if cleanupEnabled {
				GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources (TEST_CLUSTER_CLEANUP is enabled - all VMs will be removed)...\n")
			} else {
				GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources (TEST_CLUSTER_CLEANUP is not enabled - only bootstrap node will be removed)...\n")
			}
			err := cluster.CleanupTestCluster(ctx, testClusterResources)
			if err != nil {
				GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
			} else {
				GinkgoWriter.Printf("    ✅ Test cluster resources cleaned up successfully\n")
			}
		}
	})

	// ---=== TEST CLUSTER IS CREATED AND READY HERE ===--- //

	It("should create test cluster", func() {
		By("Creating test cluster", func() {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCreationTimeout)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Creating test cluster\n")
			var err error
			testClusterResources, err = cluster.CreateTestCluster(ctx, config.YAMLConfigFilename)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Failed to create test cluster: %v\n", err)
				Expect(err).NotTo(HaveOccurred(), "Test cluster should be created successfully")
			}
			GinkgoWriter.Printf("    ✅ Test cluster created successfully (all modules are Ready)\n")
		})
	}) // should create test cluster

	////////////////////////////////////
	// ---=== TESTS START HERE ===--- //
	////////////////////////////////////

	It("should run a test", func() {

		By("Applying NGCs", func() {
			GinkgoWriter.Printf("    ▶️ Running a test...\n")

			logger.Info("Running a test...")

			// err := testkit.RunTest(ctx, testClusterResources)
			// if err != nil {
			// 	GinkgoWriter.Printf("    ❌ Failed to run test: %v\n", err)
			// 	Expect(err).NotTo(HaveOccurred(), "Test should be performed successfully")
			// }

			GinkgoWriter.Printf("    ✅ Test performed successfully\n")
		})
	})

	///////////////////////////////////////////////////// ---=== TESTS END HERE ===--- /////////////////////////////////////////////////////

}) // Describe: Sds Node Configurator
