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
)

// These lightweight specs carry distinct Ginkgo labels so the CI label routing
// (PR labels e2e/label:<x> -> -ginkgo.label-filter) can be exercised end to end.
// They do not touch a cluster: the cluster is provisioned by the CI bootstrap job.
var _ = Describe("E2E label routing", func() {
	It("smoke check", Label("smoke"), func() {
		GinkgoWriter.Println("smoke spec running")
		Expect(true).To(BeTrue())
	})

	It("integration check", Label("integration"), func() {
		GinkgoWriter.Println("integration spec running")
		Expect(true).To(BeTrue())
	})

	It("regress check", Label("regress"), func() {
		GinkgoWriter.Println("regress spec running")
		Expect(true).To(BeTrue())
	})

	It("stress check", Label("stress-test"), func() {
		GinkgoWriter.Println("stress-test spec running")
		Expect(true).To(BeTrue())
	})
})
