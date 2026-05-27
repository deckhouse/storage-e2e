/*
Copyright 2026 Flant JSC

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

package sds_node_configurator_stress_tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	"github.com/deckhouse/storage-e2e/pkg/testkit"
)

var _ = Describe("sds-node-configurator: maximum independent LVMVolumeGroups per node", Label("stress"), Ordered, func() {
	var (
		testClusterResources *cluster.TestClusterResources
		stressResult         *testkit.MaxVGsStressResult
		stressCfg            testkit.MaxVGsStressConfig
	)

	AfterAll(func() {
		if testClusterResources != nil {
			ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCleanupTimeout)
			defer cancel()
			_ = cluster.CleanupTestCluster(ctx, testClusterResources)
		}
	})

	It("should create test cluster with sds-node-configurator", func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCreationTimeout)
		defer cancel()

		var err error
		testClusterResources, err = cluster.CreateTestCluster(ctx, config.YAMLConfigFilename)
		Expect(err).NotTo(HaveOccurred(), "test cluster should be created")
		Expect(testClusterResources.BaseKubeconfig).NotTo(BeNil(), "base cluster kubeconfig required for VirtualDisk attach")
	})

	It("should ramp independent LVMVolumeGroups on one node until target or first failing batch", func() {
		Expect(testClusterResources).NotTo(BeNil())
		runID := fmt.Sprintf("%d", time.Now().Unix())
		stressCfg = testkit.DefaultMaxVGsStressConfig(config.TestClusterNamespace, config.TestClusterStorageClass, runID)

		GinkgoWriter.Printf("\n    Stress config: target=%d batch=%d disk=%s strict=%v minReady=%d\n",
			stressCfg.Target, stressCfg.BatchSize, stressCfg.DiskSize, stressCfg.Strict, stressCfg.MinReady)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
		defer cancel()

		runner := &testkit.MaxVGsStressRunner{
			Cfg:        stressCfg,
			NestedKube: testClusterResources.Kubeconfig,
			BaseKube:   testClusterResources.BaseKubeconfig,
		}
		var err error
		stressResult, err = runner.Run(ctx)
		Expect(err).NotTo(HaveOccurred())

		printStressReport(stressResult)

		if stressCfg.Strict {
			Expect(stressResult.ReadyTotal).To(Equal(stressCfg.Target),
				"strict: expected %d Ready LVMVolumeGroups on %s", stressCfg.Target, stressResult.NodeName)
		} else {
			Expect(stressResult.ReadyTotal).To(BeNumerically(">=", stressCfg.MinReady),
				"probe: at least %d Ready (got %d/%d, stopped early=%v)",
				stressCfg.MinReady, stressResult.ReadyTotal, stressCfg.Target, stressResult.StoppedEarly)
		}
	})

	AfterEach(func() {
		if testClusterResources == nil || stressResult == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), config.ClusterCleanupTimeout)
		defer cancel()
		testkit.CleanupMaxVGsStress(ctx, testClusterResources.Kubeconfig, testClusterResources.BaseKubeconfig, config.TestClusterNamespace, stressResult)
		stressResult = nil
	})
})

func printStressReport(res *testkit.MaxVGsStressResult) {
	GinkgoWriter.Printf("\n========== Max independent VGs per node — report ==========\n")
	GinkgoWriter.Printf("  node: %s\n", res.NodeName)
	GinkgoWriter.Printf("  Ready / target: %d / %d (batch %d, stopped early=%v)\n",
		res.ReadyTotal, res.Target, res.BatchSize, res.StoppedEarly)
	for _, s := range res.Slots {
		phase := "Pending"
		if s.Ready {
			phase = "Ready"
		}
		GinkgoWriter.Printf("  [%02d] disk=%s lvg=%s vg=%s bd=%s %s\n",
			s.Index, s.DiskName, s.LVGName, s.VGName, s.BDName, phase)
	}
	GinkgoWriter.Println("================================================================\n")
}
