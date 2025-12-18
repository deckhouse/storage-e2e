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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/apps"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/core"
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

// CheckClusterHealth checks if the deckhouse deployment pod is running with 2/2 ready replicas
// in the d8-system namespace. This function is widely used to check cluster health after certain steps.
func CheckClusterHealth(ctx context.Context, kubeconfig *rest.Config) error {
	namespace := "d8-system"
	deploymentName := "deckhouse"

	// Create deployment client
	deploymentClient, err := apps.NewDeploymentClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create deployment client: %w", err)
	}

	// Get the deployment
	deployment, err := deploymentClient.Get(ctx, namespace, deploymentName)
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check if deployment has 2 ready replicas
	if deployment.Status.ReadyReplicas != 2 {
		return fmt.Errorf("deployment %s/%s has %d ready replicas, expected 2", namespace, deploymentName, deployment.Status.ReadyReplicas)
	}

	// Create pod client
	podClient, err := core.NewPodClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create pod client: %w", err)
	}

	// Get pods for the deployment using the deployment's selector
	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	pods, err := podClient.ListByLabelSelector(ctx, namespace, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list pods for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Check that we have exactly 2 pods and both are running
	if len(pods.Items) != 1 {
		return fmt.Errorf("expected 1 pods for deployment %s/%s, found %d", namespace, deploymentName, len(pods.Items))
	}

	// Check each pod is running and all containers are ready
	for _, pod := range pods.Items {
		if !podClient.IsRunning(ctx, &pod) {
			return fmt.Errorf("pod %s/%s is not running (phase: %s)", namespace, pod.Name, pod.Status.Phase)
		}

		if !podClient.AllContainersReady(ctx, &pod) {
			return fmt.Errorf("pod %s/%s does not have all containers ready", namespace, pod.Name)
		}
	}

	return nil
}

// ConnectToCluster establishes SSH connection to the test cluster master through the base cluster master,
// retrieves kubeconfig, and sets up port forwarding tunnel.
// The SSH tunnel remains active after this function returns (it's stored in the returned resources).
// Returns the test cluster resources including the tunnel that must be kept alive.
// Note: This function does NOT check cluster health - use CheckClusterHealth() for that.
func ConnectToCluster(ctx context.Context, baseSSHClient ssh.SSHClient, testClusterMasterIP string) (*TestClusterResources, error) {
	if baseSSHClient == nil {
		return nil, fmt.Errorf("baseSSHClient cannot be nil")
	}
	if testClusterMasterIP == "" {
		return nil, fmt.Errorf("testClusterMasterIP cannot be empty")
	}

	// Step 1: Create SSH client to test cluster master through base cluster master (jump host)
	testSSHClient, err := ssh.NewClientWithJumpHost(
		config.SSHUser, config.SSHHost, config.SSHKeyPath, // jump host (base cluster master)
		config.VMSSHUser, testClusterMasterIP, config.SSHKeyPath, // target (test cluster master)
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client to test cluster master: %w", err)
	}

	// Step 2: Establish SSH tunnel with port forwarding 6445:127.0.0.1:6445
	tunnelInfo, err := ssh.EstablishSSHTunnel(ctx, testSSHClient, "6445")
	if err != nil {
		testSSHClient.Close()
		return nil, fmt.Errorf("failed to establish SSH tunnel to test cluster: %w", err)
	}

	// Step 3: Get kubeconfig from test cluster master
	_, kubeconfigPath, err := internalcluster.GetKubeconfig(ctx, testClusterMasterIP, config.VMSSHUser, config.SSHKeyPath, testSSHClient)
	if err != nil {
		tunnelInfo.StopFunc()
		testSSHClient.Close()
		return nil, fmt.Errorf("failed to get kubeconfig from test cluster: %w", err)
	}

	// Step 4: Update kubeconfig to use the tunnel port (6445)
	if err := internalcluster.UpdateKubeconfigPort(kubeconfigPath, tunnelInfo.LocalPort); err != nil {
		tunnelInfo.StopFunc()
		testSSHClient.Close()
		return nil, fmt.Errorf("failed to update kubeconfig port: %w", err)
	}

	// Rebuild rest.Config from updated kubeconfig file
	kubeconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		tunnelInfo.StopFunc()
		testSSHClient.Close()
		return nil, fmt.Errorf("failed to rebuild kubeconfig from file: %w", err)
	}

	// Return resources with active tunnel
	// Note: The test will use Eventually to check cluster health with CheckClusterHealth
	return &TestClusterResources{
		SSHClient:      testSSHClient,
		Kubeconfig:     kubeconfig,
		KubeconfigPath: kubeconfigPath,
		TunnelInfo:     tunnelInfo,
	}, nil
}
