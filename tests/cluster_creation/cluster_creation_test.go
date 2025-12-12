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
	)

	BeforeEach(func(ctx SpecContext) {
		var err error
		var clusterDefinition *config.ClusterDefinition
		var kubeconfig *rest.Config
		var sshClient ssh.SSHClient

		// Stage 1: LoadConfig - verifies and parses the config from yaml file
		By("LoadConfig: Loading and verifying cluster configuration from YAML", func() {
			clusterDefinition, err = cluster.LoadClusterConfig(yamlConfigFilename)
			Expect(err).NotTo(HaveOccurred())
		})

		// Stage 2: Establish SSH connection to base cluster (reused for getting kubeconfig)
		By("Establishing ssh connection to the base cluster", func() {
			sshClient, err = ssh.NewClient(baseClusterUser, baseClusterMasterIP, baseClusterSSHPrivateKey)
			Expect(err).NotTo(HaveOccurred())
		})

		// Stage 3: Getting kubeconfig from base cluster (reusing SSH connection to avoid double passphrase prompt)
		By("Get kubeconfig: Getting kubeconfig from the base cluster", func() {
			kubeconfig, err = cluster.GetKubeconfig(baseClusterMasterIP, baseClusterUser, baseClusterSSHPrivateKey, sshClient)
			Expect(err).NotTo(HaveOccurred())
		})

		By("Establishing ssh tunnel to the base cluster with 6445 port forwarding", func() {

		})

		_ = sshClient         // TODO: use sshClient
		_ = clusterDefinition // TODO: use clusterDefinition
		_ = kubeconfig        // TODO: use kubeconfig
	}) // BeforeEach: Cluster Creation

	It("should create a test cluster", func() {
		By("Creating a test cluster", func() {
			fmt.Println("Creating a test cluster")

		})
	}) // It: should create a test cluster
}) // Describe: Cluster Creation
