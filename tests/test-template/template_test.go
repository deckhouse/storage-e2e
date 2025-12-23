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

package test_template

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

var _ = Describe("Template Test", Ordered, func() {
	var (
		testClusterResources *cluster.TestClusterResources
	)

	BeforeAll(func() {
		By("Validating environment variables", func() {
			GinkgoWriter.Printf("    ▶️ Validating environment variables\n")
			err := config.ValidateEnvironment()
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("    ✅ Environment variables validated successfully\n")
		})
	})

	AfterAll(func() {
		// Cleanup test cluster resources
		if testClusterResources != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			GinkgoWriter.Printf("    ▶️ Cleaning up test cluster resources...\n")
			err := cluster.CleanupTestCluster(ctx, testClusterResources)
			if err != nil {
				GinkgoWriter.Printf("    ⚠️  Warning: Cleanup errors occurred: %v\n", err)
			} else {
				GinkgoWriter.Printf("    ✅ Test cluster resources cleaned up successfully\n")
			}
		}
	})

	// ---=== TEST CLUSTER IS CREATED AND GOT READY HERE ===--- //

	It("should create test cluster and wait for it to become ready", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		By("Creating test cluster", func() {
			GinkgoWriter.Printf("    ▶️ Creating test cluster (this may take up to 60 minutes)...\n")
			Eventually(func() error {
				var err error
				testClusterResources, err = cluster.CreateTestCluster(ctx, config.YAMLConfigFilename)
				return err
			}).WithTimeout(60*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
				"Test cluster should be created within 60 minutes")
			GinkgoWriter.Printf("    ✅ Test cluster created successfully\n")
		})

		By("Waiting for test cluster to become ready", func() {
			GinkgoWriter.Printf("    ▶️ Waiting for all modules to be ready in test cluster...\n")
			Eventually(func() error {
				return cluster.WaitForTestClusterReady(ctx, testClusterResources)
			}).WithTimeout(60*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
				"Test cluster should become ready within 60 minutes")
			GinkgoWriter.Printf("    ✅ Test cluster is ready (all modules are Ready)\n")
		})
	}) // should create test cluster

	///////////////////////////////////////////////////// ---=== TESTS START HERE ===--- /////////////////////////////////////////////////////

	It("should perform a test", func() {
		By("A test", func() {
			GinkgoWriter.Printf("    ▶️ Performing a test...\n")
			// TODO: Perform a test
			GinkgoWriter.Printf("    ✅ Test performed successfully\n")
		})
	}) // should perform a test

	///////////////////////////////////////////////////// ---=== TESTS END HERE ===--- /////////////////////////////////////////////////////

}) // Describe: Template Test
