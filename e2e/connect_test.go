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

package e2e

import (
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	e2esdk "github.com/deckhouse/storage-e2e/pkg/e2e"
)

// Smoke check for the pkg/e2e SDK against the cluster provisioned by the CI
// bootstrap job: Connect (provider select + health check + Lease lock), the
// typed client, and the NodeExecutor capability end to end.
var _ = Describe("SDK Connect", func() {
	It("attaches to the provisioned cluster and reaches its nodes", Label("smoke"), func(ctx SpecContext) {
		if os.Getenv("E2E_TEST_CLUSTER_PROVIDER") == "" {
			Skip("E2E_TEST_CLUSTER_PROVIDER is not set — no provisioned cluster to attach to")
		}

		cl, err := e2esdk.Connect(ctx, e2esdk.WithTestName("storage-e2e-self-test"))
		Expect(err).NotTo(HaveOccurred(), "e2e.Connect should attach to the provisioned cluster")
		DeferCleanup(func(ctx SpecContext) {
			Expect(cl.Close(ctx)).To(Succeed())
		})

		GinkgoWriter.Printf("connected via provider %q\n", cl.ProviderName())

		cs, err := cl.Clientset()
		Expect(err).NotTo(HaveOccurred())

		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(nodes.Items).NotTo(BeEmpty(), "the test cluster should report its nodes")

		nodeName := nodes.Items[0].Name
		res, err := cl.Nodes().Exec(ctx, nodeName, "hostname")
		Expect(err).NotTo(HaveOccurred(), "NodeExecutor should reach node %s", nodeName)
		Expect(res.ExitCode).To(BeZero(), "stderr: %s", string(res.Stderr))
		Expect(strings.TrimSpace(string(res.Stdout))).NotTo(BeEmpty())
	}, SpecTimeout(20*time.Minute))
})
