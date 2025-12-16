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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

var _ = Describe("Cluster Creation Test", Ordered, func() {
	var (
		testClusterResources *cluster.TestClusterResources
		ctx                  context.Context = context.Background()
	)

	BeforeAll(func() {
		By("Validating environment variables", func() {
			GinkgoWriter.Printf("    ▶️ Validating environment variables\n")
			err := config.ValidateEnvironment()
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Environment variables validated successfully\n")
		})
		// DeferCleanup: Clean up all resources in reverse order of creation - analog of AfterAll() in Ginkgo
		DeferCleanup(func() {
			if testClusterResources != nil {
				By("Cleaning up test cluster resources", func() {
					GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources\n")
					err := cluster.CleanupTestCluster(testClusterResources)
					Expect(err).NotTo(HaveOccurred(), "CleanupTestCluster should succeed")
					GinkgoWriter.Printf("    ✅ Test cluster resources cleaned up successfully\n")
				})
			}
		})
	})

	It("should successfully create test cluster", func() {
		By("Creating test cluster connection", func() {
			GinkgoWriter.Printf("    ▶️ Creating test cluster connection\n")
			var err error
			yamlConfigFilename := config.YAMLConfigFilename
			testClusterResources, err = cluster.CreateTestCluster(
				ctx,
				yamlConfigFilename,
			)
			Expect(err).NotTo(HaveOccurred(), "CreateTestCluster should succeed")
			Expect(testClusterResources).NotTo(BeNil(), "TestClusterResources should not be nil")
			GinkgoWriter.Printf("    ✅ Test cluster connection created successfully\n")
		})
	})

	It("should get all test cluster resources", func() {
		Expect(testClusterResources).NotTo(BeNil())
		Expect(testClusterResources.SSHClient).NotTo(BeNil(), "SSH client should be created")
		Expect(testClusterResources.Kubeconfig).NotTo(BeNil(), "Kubeconfig should be created")
		Expect(testClusterResources.KubeconfigPath).NotTo(BeEmpty(), "Kubeconfig path should be set")
		Expect(testClusterResources.TunnelInfo).NotTo(BeNil(), "Tunnel info should be created")
		Expect(testClusterResources.TunnelInfo.LocalPort).To(Equal(6445), "Local port should be exactly 6445")
		Expect(testClusterResources.ClusterDefinition).NotTo(BeNil(), "Cluster definition should be loaded")
		GinkgoWriter.Printf("    ✅ All test cluster resources verified successfully\n")
	})

}) // Describe: Cluster Creation
