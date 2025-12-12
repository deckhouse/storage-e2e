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
	"fmt"

	"k8s.io/client-go/rest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

var _ = Describe("Cluster Creation", func() {
	var (
		yamlConfigFilename       string = "cluster_creation_test.yml"
		baseClusterMasterIP      string = "172.17.1.67"
		baseClusterUser          string = "tfadm"
		baseClusterSSHPrivateKey string = "~/.ssh/id_rsa"

		err            error
		sshclient      ssh.SSHClient
		kubeconfig     *rest.Config
		kubeconfigPath string
		tunnelinfo     *ssh.TunnelInfo
	)

	BeforeEach(func(ctx SpecContext) {
		var err error
		var clusterDefinition *config.ClusterDefinition
		var tunnelinfo *ssh.TunnelInfo

		// Stage 1: LoadConfig - verifies and parses the config from yaml file
		By("LoadConfig: Loading and verifying cluster configuration from YAML", func() {
			clusterDefinition, err = cluster.LoadClusterConfig(yamlConfigFilename)
			Expect(err).NotTo(HaveOccurred())
		})

		// Clean up tunnel when test completes
		DeferCleanup(func() {
			if tunnelinfo != nil && tunnelinfo.StopFunc != nil {
				_ = tunnelinfo.StopFunc()
			}
		})

		_ = clusterDefinition // TODO: use clusterDefinition

	}) // BeforeEach: Cluster Creation

	// Stage 2: Establish SSH connection to base cluster (reused for getting kubeconfig)
	It("should establish ssh connection to the base cluster", func() {
		sshclient, err = ssh.NewClient(baseClusterUser, baseClusterMasterIP, baseClusterSSHPrivateKey)
		Expect(err).NotTo(HaveOccurred())
	})

	// Stage 3: Getting kubeconfig from base cluster (reusing SSH connection to avoid double passphrase prompt)

	It("should get kubeconfig from the base cluster", func() {
		kubeconfig, kubeconfigPath, err = cluster.GetKubeconfig(baseClusterMasterIP, baseClusterUser, baseClusterSSHPrivateKey, sshclient)
		Expect(err).NotTo(HaveOccurred())
	})

	// Stage 4: Establish SSH tunnel with port forwarding

	It("should establish ssh tunnel to the base cluster with port forwarding", func() {
		tunnelinfo, err = ssh.EstablishSSHTunnel(sshclient, "6445")
		Expect(err).NotTo(HaveOccurred())
		Expect(tunnelinfo).NotTo(BeNil())
		Expect(tunnelinfo.LocalPort).To(BeNumerically(">=", 1024))

		// Update kubeconfig if port differs from 6445
		if tunnelinfo.LocalPort != 6445 {
			err = cluster.UpdateKubeconfigPort(kubeconfigPath, tunnelinfo.LocalPort)
		}
	})

	It("should query K8s cluster", func() {
		fmt.Println("querying K8s cluster")

	})

	_ = kubeconfig // TODO: use kubeconfig

}) // Describe: Cluster Creation
