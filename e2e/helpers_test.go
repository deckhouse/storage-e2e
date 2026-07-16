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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e2esdk "github.com/deckhouse/storage-e2e/pkg/e2e"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// connectAndPickWorker attaches to the provisioned cluster, registers its
// cleanup, and returns the first worker node to exercise the checks on.
func connectAndPickWorker(ctx SpecContext, testName string) (*e2esdk.Cluster, string) {
	GinkgoHelper()

	cl, err := e2esdk.Connect(ctx, e2esdk.WithTestName(testName))
	Expect(err).NotTo(HaveOccurred(), "e2e.Connect should attach to the provisioned cluster")
	DeferCleanup(func(ctx SpecContext) {
		Expect(cl.Close(ctx)).To(Succeed())
	})

	GinkgoWriter.Printf("connected via provider %q\n", cl.ProviderName())

	workers, err := kubernetes.GetWorkerNodes(ctx, cl.RESTConfig())
	Expect(err).NotTo(HaveOccurred())
	Expect(workers).NotTo(BeEmpty(), "the test cluster should have worker nodes")
	return cl, workers[0].Name
}
