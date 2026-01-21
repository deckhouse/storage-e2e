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

package csi_huawei_stress_tests

import (
	"context"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/testkit"
)

var _ = Describe("Csi Huawei Stress Tests", Ordered, func() {
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

	It("should create NGCs", func() {
		ctx := context.Background()

		// Resolve file path relative to test directory (same approach as CreateTestCluster)
		// runtime.Caller(0) gets this test file's location
		_, callerFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "Failed to determine test file path")
		testDir := filepath.Dir(callerFile)

		yamlFilePathNGCs := filepath.Join(testDir, "files", "ngc.yml")

		By("Applying NGCs", func() {
			GinkgoWriter.Printf("    ▶️ Creating NGCs...\n")

			// Apply the YAML manifest
			err := kubernetes.CreateYAMLFile(ctx, testClusterResources.Kubeconfig, yamlFilePathNGCs, "")
			Expect(err).NotTo(HaveOccurred(), "Failed to apply YAML resources")

			GinkgoWriter.Printf("    ✅ Resources created successfully\n")
		})
	})

	It("should enable csi-huawei module with dependencies", func() {
		ctx := context.Background()

		By("Enabling csi-huawei module with dependencies", func() {
			GinkgoWriter.Printf("    ▶️ Enabling modules: csi-huawei and its dependencies...\n")

			// Define modules to enable
			// csi-huawei depends on snapshot-controller, so we enable both
			modulesToEnable := []*config.ModuleConfig{
				{
					Name:     "snapshot-controller",
					Enabled:  true,
					Settings: map[string]interface{}{
						// Module-specific settings go here
						// Example:
						// enableThinProvisioning: true,
					},
					Dependencies:       []string{},
					ModulePullOverride: "main", // imageTag: "mr30", "main", "pr123", etc.
				},
				{
					Name:     "csi-huawei",
					Enabled:  true,
					Settings: map[string]interface{}{
						// Module-specific settings go here
					},
					Dependencies:       []string{"snapshot-controller"}, // Explicit dependencies
					ModulePullOverride: "mr47",                          // imageTag: "mr30", "main", "pr123", etc.
				},
			}

			// Create cluster definition with modules to enable
			// Use the same registry repo as the test cluster was created with
			clusterDef := &config.ClusterDefinition{
				DKPParameters: config.DKPParameters{
					Modules:      modulesToEnable,
					RegistryRepo: testClusterResources.ClusterDefinition.DKPParameters.RegistryRepo,
				},
			}

			// Enable and configure modules
			// This will handle dependencies automatically through topological sort
			// and wait for each level to become Ready before proceeding to the next
			err := kubernetes.EnableAndConfigureModules(
				ctx,
				testClusterResources.Kubeconfig,
				clusterDef,
				testClusterResources.SSHClient,
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to enable and configure modules")

		})
	})

	It("should create Huawei storage resources", func() {
		ctx := context.Background()

		// Resolve file path relative to test directory (same approach as CreateTestCluster)
		// runtime.Caller(0) gets this test file's location
		_, callerFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "Failed to determine test file path")
		testDir := filepath.Dir(callerFile)
		yamlFilePath := filepath.Join(testDir, "files", "csi-huawei-cr.yml")

		By("Applying HuaweiStorageConnection and HuaweiStorageClass", func() {
			GinkgoWriter.Printf("    ▶️ Creating Huawei storage resources...\n")

			// Apply the YAML manifest
			err := kubernetes.CreateYAMLFile(ctx, testClusterResources.Kubeconfig, yamlFilePath, "")
			Expect(err).NotTo(HaveOccurred(), "Failed to apply YAML resources")

			GinkgoWriter.Printf("    ✅ Resources created successfully\n")
		})

		By("Waiting for StorageClass to become available", func() {
			GinkgoWriter.Printf("    ▶️ Waiting for StorageClass hsclass-200...\n")

			err := kubernetes.WaitForStorageClass(ctx, testClusterResources.Kubeconfig, "hsclass-200", 10*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "StorageClass hsclass-200 not available")

			GinkgoWriter.Printf("    ✅ StorageClass is available\n")
		})

		// By("Patching StorageClass with volume snapshot annotation", func() {
		// 	GinkgoWriter.Printf("    ▶️ Adding volume snapshot class annotation to StorageClass...\n")

		// 	// Patch StorageClass with volume snapshot class annotation
		// 	// The snapshot class name typically matches the storage class name
		// 	err := kubernetes.PatchStorageClassWithSnapshotAnnotation(ctx, testClusterResources.Kubeconfig, "hsclass-200", "hsclass-200")
		// 	Expect(err).NotTo(HaveOccurred(), "Failed to patch StorageClass with snapshot annotation")

		// 	GinkgoWriter.Printf("    ✅ StorageClass patched successfully\n")
		// })

	})

	It("should run flog stress test", func() {
		// Use a timeout context for the stress test (30 minutes should be enough)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		By("Running flog stress test with PVC resize (60 minutes timeout)", func() {
			GinkgoWriter.Printf("    ▶️ Running flog stress test...\n")

			// Configure stress test
			stressConfig := testkit.DefaultConfig()
			stressConfig.Namespace = "stress-test-flog"
			stressConfig.StorageClassName = "hsclass-200"
			stressConfig.PVCSize = "100Mi"
			stressConfig.PodsCount = 100
			stressConfig.Iterations = 1
			stressConfig.Mode = testkit.ModeFlog
			stressConfig.PVCSizeAfterResize = "200Mi"
			stressConfig.Cleanup = true
			// Set a reasonable timeout: 5 seconds * 500 attempts = 41 minutes, we use more
			stressConfig.MaxAttempts = 500
			stressConfig.Interval = 5 * time.Second

			// Create and run stress test
			runner, err := testkit.NewStressTestRunner(stressConfig, testClusterResources.Kubeconfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create stress test runner")

			err = runner.Run(ctx)
			Expect(err).NotTo(HaveOccurred(), "Stress test failed")

			GinkgoWriter.Printf("    ✅ Stress test completed successfully\n")
		})
	})

	It("should run snapshot/resize/clone stress test", func() {
		// Use a timeout context for the stress test (60 minutes for complex test)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		By("Patching StorageClass with volume snapshot annotation", func() {
			GinkgoWriter.Printf("    ▶️ Adding volume snapshot class annotation to StorageClass...\n")

			// Patch StorageClass with volume snapshot class annotation
			// The snapshot class name typically matches the storage class name
			err := kubernetes.PatchStorageClassWithSnapshotAnnotation(ctx, testClusterResources.Kubeconfig, "hsclass-200", "hsclass-200")
			Expect(err).NotTo(HaveOccurred(), "Failed to patch StorageClass with snapshot annotation")

			GinkgoWriter.Printf("    ✅ StorageClass patched successfully\n")
		})

		By("Running snapshot, resize, and clone stress test (60 minutes timeout)", func() {
			GinkgoWriter.Printf("    ▶️ Running complex stress test...\n")

			// Configure comprehensive stress test
			stressConfig := testkit.DefaultConfig()
			stressConfig.Namespace = "stress-test-complex"
			stressConfig.StorageClassName = "hsclass-200"
			stressConfig.PVCSize = "100Mi"
			stressConfig.PodsCount = 100
			stressConfig.Iterations = 1
			stressConfig.Mode = testkit.ModeSnapshotResizeCloning
			stressConfig.SnapshotsPerPVC = 2
			stressConfig.PVCSizeAfterResize = "200Mi"
			stressConfig.PVCSizeAfterResizeStage2 = "300Mi"
			stressConfig.TestOrder = []testkit.TestStep{
				testkit.StepRestoreFromSnapshot,
				testkit.StepResize,
				testkit.StepClone,
			}
			stressConfig.Cleanup = true
			// Set a reasonable timeout: 5 seconds * 720 attempts = 60 minutes max
			stressConfig.MaxAttempts = 500
			stressConfig.Interval = 5 * time.Second

			// Create and run stress test
			runner, err := testkit.NewStressTestRunner(stressConfig, testClusterResources.Kubeconfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create stress test runner")

			err = runner.Run(ctx)
			Expect(err).NotTo(HaveOccurred(), "Complex stress test failed")

			GinkgoWriter.Printf("    ✅ Complex stress test completed successfully\n")
		})
	})

	///////////////////////////////////////////////////// ---=== TESTS END HERE ===--- /////////////////////////////////////////////////////

}) // Describe: Csi Huawei Stress Tests
