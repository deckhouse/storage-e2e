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
)

// Live checks for the NodeExecutor capability of the pkg/e2e SDK: stdout and
// stderr are captured separately, non-zero exit codes are reported without an
// error, and passwordless sudo is available on the node.
var _ = Describe("Node executor", func() {
	It("honors the exec contract on a worker node", Label("nodes"), func(ctx SpecContext) {
		if os.Getenv("E2E_TEST_CLUSTER_PROVIDER") == "" {
			Skip("E2E_TEST_CLUSTER_PROVIDER is not set — no provisioned cluster to attach to")
		}

		cl, nodeName := connectAndPickWorker(ctx, "storage-e2e-node-executor")
		nodes := cl.Nodes()

		By("capturing stdout and stderr separately")
		res, err := nodes.Exec(ctx, nodeName, "echo -n e2e-stdout; echo -n e2e-stderr 1>&2")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Stdout)).To(Equal("e2e-stdout"))
		Expect(string(res.Stderr)).To(Equal("e2e-stderr"))
		Expect(res.ExitCode).To(BeZero())

		By("propagating a non-zero exit code without an error")
		res, err = nodes.Exec(ctx, nodeName, "exit 42")
		Expect(err).NotTo(HaveOccurred(), "a completed command with a non-zero exit code is not an error")
		Expect(res.ExitCode).To(Equal(42))

		By("running sudo non-interactively")
		res, err = nodes.Exec(ctx, nodeName, "sudo -n true")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ExitCode).To(BeZero(),
			"passwordless sudo unavailable, stderr: %s", strings.TrimSpace(string(res.Stderr)))
	}, SpecTimeout(30*time.Minute))
})
