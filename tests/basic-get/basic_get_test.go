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

package basic_get

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// expandHome turns a leading ~ into the user's home directory.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Basic smoke test: SSH to an existing cluster master (through the jump host when SSH_JUMP_HOST is set),
// open a tunnel, load its kubeconfig, list nodes, then disconnect. This deliberately uses the low-level
// ConnectToCluster helper to skip cluster-health polling, lock acquisition and the base-cluster step —
// none of which are needed just to "get and exit".
var _ = Describe("Basic Get", Ordered, func() {
	var res *cluster.TestClusterResources

	BeforeAll(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		By("Connecting to existing cluster (SSH + tunnel + kubeconfig)", func() {
			key := expandHome(config.SSHPrivateKey)

			opts := cluster.ConnectClusterOptions{
				SSHUser:             config.SSHUser,
				SSHHost:             config.SSHHost,
				SSHKeyPath:          key,
				KubeconfigOutputDir: config.E2ETempDir,
			}
			if config.SSHJumpHost != "" {
				jumpUser := config.SSHJumpUser
				if jumpUser == "" {
					jumpUser = config.SSHUser
				}
				jumpKey := key
				if config.SSHJumpKeyPath != "" {
					jumpKey = expandHome(config.SSHJumpKeyPath)
				}
				opts = cluster.ConnectClusterOptions{
					SSHUser:             jumpUser,
					SSHHost:             config.SSHJumpHost,
					SSHKeyPath:          jumpKey,
					UseJumpHost:         true,
					JumpHostUser:        jumpUser,
					JumpHostHost:        config.SSHJumpHost,
					JumpHostKeyPath:     jumpKey,
					TargetUser:          config.SSHUser,
					TargetHost:          config.SSHHost,
					TargetKeyPath:       key,
					KubeconfigOutputDir: config.E2ETempDir,
				}
			}

			var err error
			res, err = cluster.ConnectToCluster(ctx, opts)
			Expect(err).NotTo(HaveOccurred(), "Failed to connect to existing cluster")
			Expect(res).NotTo(BeNil())
			Expect(res.Kubeconfig).NotTo(BeNil())
		})
	})

	AfterAll(func() {
		if res == nil {
			return
		}
		By("Closing tunnel and SSH connection", func() {
			if res.TunnelInfo != nil && res.TunnelInfo.StopFunc != nil {
				res.TunnelInfo.StopFunc()
			}
			if res.SSHClient != nil {
				res.SSHClient.Close()
			}
		})
	})

	It("lists cluster nodes", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		clientset, err := kubernetes.NewClientsetWithRetry(ctx, res.Kubeconfig)
		Expect(err).NotTo(HaveOccurred(), "Failed to build kubernetes clientset")

		nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to list nodes")
		Expect(nodes.Items).NotTo(BeEmpty(), "Cluster should have at least one node")

		GinkgoWriter.Printf("    ✅ Got %d node(s):\n", len(nodes.Items))
		for _, n := range nodes.Items {
			GinkgoWriter.Printf("      - %s\n", n.Name)
		}
	})
})
