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

package cluster

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/rest"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

// TestClusterResources holds all resources created for a test cluster connection
type TestClusterResources struct {
	SSHClient         ssh.SSHClient
	Kubeconfig        *rest.Config
	KubeconfigPath    string
	TunnelInfo        *ssh.TunnelInfo
	ClusterDefinition *config.ClusterDefinition
}

// CreateTestCluster establishes a connection to a test cluster by:
// 1. Loading cluster configuration from YAML
// 2. Establishing SSH connection to the base cluster
// 3. Retrieving kubeconfig from the base cluster
// 4. Establishing SSH tunnel with port forwarding
//
// It returns all the resources needed to interact with the cluster.
// SSH credentials are obtained from environment variables via config functions.
func CreateTestCluster(
	ctx context.Context,
	yamlConfigFilename string,
) (*TestClusterResources, error) {
	// Stage 1: Load cluster configuration from YAML
	clusterDefinition, err := internalcluster.LoadClusterConfig(yamlConfigFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	// Get SSH credentials from environment variables
	sshHost := config.SSHHost
	sshUser := config.SSHUser
	sshKeyPath := config.SSHKeyPath

	// Stage 2: Establish SSH connection to base cluster
	sshClient, err := ssh.NewClient(sshUser, sshHost, sshKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	// Stage 3: Get kubeconfig from base cluster
	// Use a timeout context for kubeconfig retrieval
	kubeconfigCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	kubeconfig, kubeconfigPath, err := internalcluster.GetKubeconfig(
		kubeconfigCtx,
		sshHost,
		sshUser,
		sshKeyPath,
		sshClient,
	)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Stage 4: Establish SSH tunnel with port forwarding
	tunnelInfo, err := ssh.EstablishSSHTunnel(ctx, sshClient, "6445")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("failed to establish SSH tunnel: %w", err)
	}

	return &TestClusterResources{
		SSHClient:         sshClient,
		Kubeconfig:        kubeconfig,
		KubeconfigPath:    kubeconfigPath,
		TunnelInfo:        tunnelInfo,
		ClusterDefinition: clusterDefinition,
	}, nil
}

// CleanupTestCluster cleans up all resources created by CreateTestCluster
func CleanupTestCluster(resources *TestClusterResources) error {
	var errs []error

	// Stop SSH tunnel first (must be done before closing SSH client)
	if resources.TunnelInfo != nil && resources.TunnelInfo.StopFunc != nil {
		if err := resources.TunnelInfo.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop SSH tunnel: %w", err))
		}
	}

	// Close SSH client connection
	if resources.SSHClient != nil {
		if err := resources.SSHClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close SSH client: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	return nil
}
