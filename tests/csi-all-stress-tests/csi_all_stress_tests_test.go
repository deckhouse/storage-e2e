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

package csi_all_stress_tests

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/testkit"
)

var _ = Describe("All CSIs Stress Tests", Ordered, func() {
	var (
		testClusterResources *cluster.TestClusterResources
		testDir              string
		crFiles              []string
		crFilesDir           string
		storageClassNames    []string
	)

	BeforeAll(func() {
		By("Setting up test variables", func() {
			_, callerFile, _, ok := runtime.Caller(0)
			Expect(ok).To(BeTrue(), "Failed to determine test file path")
			testDir = filepath.Dir(callerFile)

			crFiles = []string{"csi-huawei-cr.yml", "csi-hpe-cr.yml", "csi-netapp-cr.yml"}
			crFilesDir = filepath.Join(testDir, "files")

			storageClassNames = []string{"hsclass-200", "hpe-iscsi", "csi-netapp-sc1"}
		})

		By("Validating environment variables in CR files", func() {
			var allUnsetVars []string

			for _, fileName := range crFiles {
				filePath := filepath.Join(crFilesDir, fileName)

				// Skip if file doesn't exist
				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					continue
				}

				content, err := os.ReadFile(filePath)
				Expect(err).NotTo(HaveOccurred(), "Failed to read file: "+fileName)

				unsetVars := kubernetes.FindUnsetEnvVars(string(content))
				if len(unsetVars) > 0 {
					GinkgoWriter.Printf("    ❌ %s requires env vars: %v\n", fileName, unsetVars)
					allUnsetVars = append(allUnsetVars, unsetVars...)
				}
			}

			Expect(allUnsetVars).To(BeEmpty(), "Environment variables for custom resources are not set: "+strings.Join(allUnsetVars, ", "))
		})

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

	It("should create NGCs and wait for nodes to be labeled", func() {
		// Use 10 minute timeout for NGCs to be applied and nodes to be labeled
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		yamlFilePathNGCs := filepath.Join(testDir, "files", "ngc.yml")

		By("Applying NGCs", func() {
			GinkgoWriter.Printf("    ▶️ Creating NGCs...\n")

			applyClient, err := kubernetes.NewApplyClient(testClusterResources.Kubeconfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create apply client")

			content, err := os.ReadFile(yamlFilePathNGCs)
			Expect(err).NotTo(HaveOccurred(), "Failed to read YAML file")

			err = applyClient.CreateYAML(ctx, string(content), "")
			Expect(err).NotTo(HaveOccurred(), "Failed to apply YAML resources")

			GinkgoWriter.Printf("    ✅ NGCs created successfully\n")
		})

		By("Waiting for all nodes to be labeled with iSCSI ready label", func() {
			// Collect all node names from cluster definition
			var nodeNames []string
			for _, master := range testClusterResources.ClusterDefinition.Masters {
				nodeNames = append(nodeNames, master.Hostname)
			}
			for _, worker := range testClusterResources.ClusterDefinition.Workers {
				nodeNames = append(nodeNames, worker.Hostname)
			}

			GinkgoWriter.Printf("    ⏳ Waiting for %d nodes to be labeled (timeout: 10 minutes)...\n", len(nodeNames))
			for _, name := range nodeNames {
				GinkgoWriter.Printf("      - %s\n", name)
			}

			labelKey := "storage.deckhouse.io/node-ready-for-iscsi"
			labelValue := "true"

			err := kubernetes.WaitForNodesLabeled(ctx, testClusterResources.Kubeconfig, nodeNames, labelKey, labelValue)
			Expect(err).NotTo(HaveOccurred(), "All nodes should be labeled with %s=%s", labelKey, labelValue)

			GinkgoWriter.Printf("    ✅ All %d nodes are labeled and ready for iSCSI\n", len(nodeNames))
		})
	})

	It("should create modules' custom resources", func() {
		ctx := context.Background()

		By("Applying all storage custom resources", func() {
			GinkgoWriter.Printf("    ▶️ Creating storage resources from %d files...\n", len(crFiles))

			applyClient, err := kubernetes.NewApplyClient(testClusterResources.Kubeconfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create apply client")

			for _, fileName := range crFiles {
				filePath := filepath.Join(crFilesDir, fileName)

				// Skip if file doesn't exist
				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					GinkgoWriter.Printf("    ⏭️  Skipping %s (file not found)\n", fileName)
					continue
				}

				GinkgoWriter.Printf("    📄 Applying %s...\n", fileName)
				err = applyClient.CreateYAMLFromFileWithEnvvars(ctx, filePath, "")
				Expect(err).NotTo(HaveOccurred(), "Failed to apply "+fileName)
			}

			GinkgoWriter.Printf("    ✅ Resources created successfully\n")
		})

		By("Waiting for StorageClasses to become available", func() {

			GinkgoWriter.Printf("    ▶️ Waiting for %d StorageClasses...\n", len(storageClassNames))

			results := kubernetes.WaitForStorageClasses(ctx, testClusterResources.Kubeconfig, storageClassNames, 10*time.Minute)

			availableCount := 0
			for scName, err := range results {
				if err != nil {
					GinkgoWriter.Printf("    ⚠️  StorageClass %s not available: %v\n", scName, err)
				} else {
					GinkgoWriter.Printf("    ✅ StorageClass %s is available\n", scName)
					availableCount++
				}
			}

			GinkgoWriter.Printf("    ✅ %d/%d StorageClasses are ready\n", availableCount, len(storageClassNames))
			Expect(availableCount).To(Equal(len(storageClassNames)), "Not all StorageClasses became available")
		})

	})

	It("should run snapshot/resize/clone stress test for all storage classes", func() {

		testResults := make(map[string]error)

		for _, scName := range storageClassNames {
			func(storageClassName string) {
				// Use a timeout context for each stress test
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
				defer cancel()

				By("Running snapshot, resize, and clone stress test for "+storageClassName+" (45 minutes timeout)", func() {
					GinkgoWriter.Printf("    ▶️ Running complex stress test for %s...\n", storageClassName)

					// Configure comprehensive stress test
					stressConfig := testkit.DefaultConfig()
					stressConfig.Namespace = "stress-test-" + storageClassName
					stressConfig.StorageClassName = storageClassName
					stressConfig.PVCSize = "300Mi"
					stressConfig.PodsCount = 100
					stressConfig.Iterations = 1
					stressConfig.Mode = testkit.ModeSnapshotResizeCloning
					stressConfig.SnapshotsPerPVC = 1
					stressConfig.PVCSizeAfterResize = "350Mi"
					stressConfig.PVCSizeAfterResizeStage2 = "400Mi"
					stressConfig.TestOrder = []testkit.TestStep{
						testkit.StepRestoreFromSnapshot,
						testkit.StepResize,
						testkit.StepClone,
					}
					stressConfig.Cleanup = true
					stressConfig.MaxAttempts = 500
					stressConfig.Interval = 5 * time.Second

					// Create and run stress test
					runner, err := testkit.NewStressTestRunner(stressConfig, testClusterResources.Kubeconfig)
					if err != nil {
						GinkgoWriter.Printf("    ❌ Failed to create stress test runner for %s: %v\n", storageClassName, err)
						testResults[storageClassName] = err
						return
					}

					err = runner.Run(ctx)
					if err != nil {
						GinkgoWriter.Printf("    ❌ Stress test failed for %s: %v\n", storageClassName, err)
						testResults[storageClassName] = err
						return
					}

					GinkgoWriter.Printf("    ✅ Stress test completed successfully for %s\n", storageClassName)
					testResults[storageClassName] = nil
				})
			}(scName)
		}

		// Report summary
		passedCount := 0
		failedCount := 0
		var failedStorageClasses []string

		for scName, err := range testResults {
			if err == nil {
				passedCount++
			} else {
				failedCount++
				failedStorageClasses = append(failedStorageClasses, scName)
			}
		}

		GinkgoWriter.Printf("\n    📊 Stress Test Summary:\n")
		GinkgoWriter.Printf("    ✅ Passed: %d/%d\n", passedCount, len(storageClassNames))
		GinkgoWriter.Printf("    ❌ Failed: %d/%d\n", failedCount, len(storageClassNames))

		if failedCount > 0 {
			GinkgoWriter.Printf("    Failed storage classes: %v\n", failedStorageClasses)
			Expect(failedCount).To(Equal(0), "Some stress tests failed")
		}
	})

	It("should add two 60GB disks to first master VM and mount them", func() {
		// Use 10 minute timeout for the entire operation
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		By("Validating cluster resources", func() {
			Expect(testClusterResources).NotTo(BeNil(), "testClusterResources should not be nil")
			Expect(testClusterResources.ClusterDefinition).NotTo(BeNil(), "ClusterDefinition should not be nil")
			Expect(testClusterResources.BaseKubeconfig).NotTo(BeNil(), "BaseKubeconfig should not be nil")
			Expect(testClusterResources.SSHClient).NotTo(BeNil(), "SSHClient should not be nil")
			Expect(len(testClusterResources.ClusterDefinition.Masters)).To(BeNumerically(">", 0), "At least one master should exist")
		})

		firstMasterName := testClusterResources.ClusterDefinition.Masters[0].Hostname
		namespace := config.TestClusterNamespace
		storageClass := config.TestClusterStorageClass

		// Define disk configurations
		diskConfigs := []struct {
			name       string
			mountPoint string
		}{
			{name: firstMasterName + "-nfs-disk", mountPoint: "/mnt/nfs"},
			{name: firstMasterName + "-minio-disk", mountPoint: "/mnt/minio"},
		}

		GinkgoWriter.Printf("    ▶️ Adding two 60GB disks to first master VM: %s in namespace %s\n", firstMasterName, namespace)

		var attachResults []*kubernetes.VirtualDiskAttachmentResult

		By("Creating and attaching two 60GB VirtualDisks to VM", func() {
			for _, diskCfg := range diskConfigs {
				attachConfig := kubernetes.VirtualDiskAttachmentConfig{
					VMName:           firstMasterName,
					Namespace:        namespace,
					DiskName:         diskCfg.name,
					DiskSize:         "60Gi",
					StorageClassName: storageClass,
				}

				attachResult, err := kubernetes.AttachVirtualDiskToVM(ctx, testClusterResources.BaseKubeconfig, attachConfig)
				Expect(err).NotTo(HaveOccurred(), "Failed to attach VirtualDisk %s to VM", diskCfg.name)
				attachResults = append(attachResults, attachResult)
				GinkgoWriter.Printf("    ✅ VirtualDisk %s created and attachment %s initiated\n", attachResult.DiskName, attachResult.AttachmentName)
			}
		})

		By("Waiting for disk attachments to complete", func() {
			for _, attachResult := range attachResults {
				GinkgoWriter.Printf("    ⏳ Waiting for disk attachment %s to complete...\n", attachResult.AttachmentName)
				err := kubernetes.WaitForVirtualDiskAttached(ctx, testClusterResources.BaseKubeconfig, namespace, attachResult.AttachmentName, 10*time.Second)
				Expect(err).NotTo(HaveOccurred(), "Disk attachment %s should complete successfully", attachResult.AttachmentName)
				GinkgoWriter.Printf("    ✅ Disk %s successfully attached\n", attachResult.DiskName)
			}
		})

		By("Formatting disks with XFS and mounting them on the node", func() {
			GinkgoWriter.Printf("    🔧 Formatting and mounting disks on %s...\n", firstMasterName)

			// Script to find new unformatted disks, format them with XFS, and mount
			// The disks appear as /dev/vdX (virtio) after hot-plug
			formatAndMountScript := `
set -e

# Find all virtio block devices that are not partitioned and not mounted
echo "Looking for new unformatted disks..."

# Get list of all vd* devices (excluding partitions)
new_disks=$(lsblk -dpno NAME,TYPE | grep 'disk' | awk '{print $1}' | grep -E '/dev/vd[b-z]$' | while read disk; do
    # Check if disk has no partitions and no filesystem
    if ! lsblk -no FSTYPE "$disk" 2>/dev/null | grep -q .; then
        echo "$disk"
    fi
done | head -2)

disk_count=$(echo "$new_disks" | grep -c '/dev/' || true)
echo "Found $disk_count new unformatted disk(s)"

if [ "$disk_count" -lt 2 ]; then
    echo "Error: Expected 2 new disks, found $disk_count"
    lsblk
    exit 1
fi

# Convert to array
disk1=$(echo "$new_disks" | head -1)
disk2=$(echo "$new_disks" | tail -1)

echo "Disk 1: $disk1 -> /mnt/nfs"
echo "Disk 2: $disk2 -> /mnt/minio"

# Format disks with XFS
echo "Formatting $disk1 with XFS..."
mkfs.xfs -f "$disk1"

echo "Formatting $disk2 with XFS..."
mkfs.xfs -f "$disk2"

# Create mount points
mkdir -p /mnt/nfs /mnt/minio

# Mount disks
echo "Mounting $disk1 to /mnt/nfs..."
mount "$disk1" /mnt/nfs

echo "Mounting $disk2 to /mnt/minio..."
mount "$disk2" /mnt/minio

# Add to fstab for persistence
disk1_uuid=$(blkid -s UUID -o value "$disk1")
disk2_uuid=$(blkid -s UUID -o value "$disk2")

echo "UUID=$disk1_uuid /mnt/nfs xfs defaults 0 0" >> /etc/fstab
echo "UUID=$disk2_uuid /mnt/minio xfs defaults 0 0" >> /etc/fstab

# Verify mounts
echo "Verifying mounts..."
df -h /mnt/nfs /mnt/minio

echo "Done! Disks formatted and mounted successfully."
`
			output, err := testClusterResources.SSHClient.Exec(ctx, formatAndMountScript)
			if err != nil {
				GinkgoWriter.Printf("    ❌ Format/mount script output:\n%s\n", output)
			}
			Expect(err).NotTo(HaveOccurred(), "Failed to format and mount disks")
			GinkgoWriter.Printf("    📋 Script output:\n%s\n", output)
			GinkgoWriter.Printf("    ✅ Disks formatted with XFS and mounted to /mnt/nfs and /mnt/minio\n")
		})
	})

	///////////////////////////////////////////////////// ---=== TESTS END HERE ===--- /////////////////////////////////////////////////////

}) // Describe: Csi All Stress Tests
