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

// CheckClusterHealth checks if the deckhouse deployment has 1 pod running with 2/2 containers ready
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

	// Check if deployment has 1 ready replica (1 pod)
	if deployment.Status.ReadyReplicas != 1 {
		return fmt.Errorf("deployment %s/%s has %d ready replicas, expected 1", namespace, deploymentName, deployment.Status.ReadyReplicas)
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

	// Check that we have exactly 1 pod
	if len(pods.Items) != 1 {
		return fmt.Errorf("expected 1 pod for deployment %s/%s, found %d", namespace, deploymentName, len(pods.Items))
	}

	// Check the pod is running and has 2/2 containers ready
	pod := pods.Items[0]
	if !podClient.IsRunning(ctx, &pod) {
		return fmt.Errorf("pod %s/%s is not running (phase: %s)", namespace, pod.Name, pod.Status.Phase)
	}

	// Verify the pod has exactly 2 containers
	if len(pod.Spec.Containers) != 2 {
		return fmt.Errorf("pod %s/%s has %d containers, expected 2", namespace, pod.Name, len(pod.Spec.Containers))
	}

	// Check all containers are ready
	if !podClient.AllContainersReady(ctx, &pod) {
		return fmt.Errorf("pod %s/%s does not have all containers ready (expected 2/2 containers ready)", namespace, pod.Name)
	}

	return nil
}

// ConnectClusterOptions defines options for connecting to a cluster
type ConnectClusterOptions struct {
	// Direct connection parameters (used when UseJumpHost is false)
	SSHUser    string
	SSHHost    string
	SSHKeyPath string

	// Jump host parameters (used when UseJumpHost is true)
	UseJumpHost     bool
	JumpHostUser    string // Optional: defaults to SSHUser if empty
	JumpHostHost    string // Optional: defaults to SSHHost if empty
	JumpHostKeyPath string // Optional: defaults to SSHKeyPath if empty
	TargetUser      string // Required when UseJumpHost is true
	TargetHost      string // Required when UseJumpHost is true (IP or hostname)
	TargetKeyPath   string // Optional: defaults to SSHKeyPath if empty
}

// ConnectToCluster establishes SSH connection to a cluster (base or test),
// retrieves kubeconfig, and sets up port forwarding tunnel.
func ConnectToCluster(ctx context.Context, opts ConnectClusterOptions) (*TestClusterResources, error) {
	// Validate required parameters
	if opts.SSHUser == "" {
		return nil, fmt.Errorf("SSHUser cannot be empty")
	}
	if opts.SSHHost == "" {
		return nil, fmt.Errorf("SSHHost cannot be empty")
	}
	if opts.SSHKeyPath == "" {
		return nil, fmt.Errorf("SSHKeyPath cannot be empty")
	}

	var sshClient ssh.SSHClient
	var masterHost string // Host/IP to use for kubeconfig retrieval
	var masterUser string // User to use for kubeconfig retrieval

	if opts.UseJumpHost {
		// Validate jump host parameters
		if opts.TargetHost == "" {
			return nil, fmt.Errorf("TargetHost is required when UseJumpHost is true")
		}
		if opts.TargetUser == "" {
			return nil, fmt.Errorf("TargetUser is required when UseJumpHost is true")
		}

		// Set defaults for jump host parameters
		jumpHostUser := opts.JumpHostUser
		if jumpHostUser == "" {
			jumpHostUser = opts.SSHUser
		}
		jumpHostHost := opts.JumpHostHost
		if jumpHostHost == "" {
			jumpHostHost = opts.SSHHost
		}
		jumpHostKeyPath := opts.JumpHostKeyPath
		if jumpHostKeyPath == "" {
			jumpHostKeyPath = opts.SSHKeyPath
		}
		targetKeyPath := opts.TargetKeyPath
		if targetKeyPath == "" {
			targetKeyPath = opts.SSHKeyPath
		}

		// Create SSH client with jump host (retry with exponential backoff)
		maxRetries := 3
		retryDelay := 2 * time.Second
		var lastErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				// Wait before retry (exponential backoff)
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("context cancelled while retrying SSH connection: %w", ctx.Err())
				case <-time.After(retryDelay):
				}
				retryDelay *= 2 // Exponential backoff
			}

			sshClient, lastErr = ssh.NewClientWithJumpHost(
				jumpHostUser, jumpHostHost, jumpHostKeyPath, // jump host
				opts.TargetUser, opts.TargetHost, targetKeyPath, // target
			)
			if lastErr == nil {
				break // Success
			}
		}
		if lastErr != nil {
			return nil, fmt.Errorf("failed to create SSH client with jump host after %d attempts: %w", maxRetries, lastErr)
		}

		masterHost = opts.TargetHost
		masterUser = opts.TargetUser
	} else {
		// Direct connection (no jump host)
		var err error
		sshClient, err = ssh.NewClient(opts.SSHUser, opts.SSHHost, opts.SSHKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH client: %w", err)
		}

		masterHost = opts.SSHHost
		masterUser = opts.SSHUser
	}

	// Step 2: Establish SSH tunnel with port forwarding 6445:127.0.0.1:6445
	// Use context.Background() for the tunnel so it persists after the function returns
	// The tunnel must remain active for subsequent operations
	tunnelInfo, err := ssh.EstablishSSHTunnel(context.Background(), sshClient, "6445")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("failed to establish SSH tunnel: %w", err)
	}

	// Step 3: Get kubeconfig from cluster master
	_, kubeconfigPath, err := internalcluster.GetKubeconfig(ctx, masterHost, masterUser, opts.SSHKeyPath, sshClient)
	if err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Step 4: Update kubeconfig to use the tunnel port (6445)
	if err := internalcluster.UpdateKubeconfigPort(kubeconfigPath, tunnelInfo.LocalPort); err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to update kubeconfig port: %w", err)
	}

	// Rebuild rest.Config from updated kubeconfig file
	kubeconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		tunnelInfo.StopFunc()
		sshClient.Close()
		return nil, fmt.Errorf("failed to rebuild kubeconfig from file: %w", err)
	}

	// Return resources with active tunnel
	// Note: The test will use Eventually to check cluster health with CheckClusterHealth
	return &TestClusterResources{
		SSHClient:      sshClient,
		Kubeconfig:     kubeconfig,
		KubeconfigPath: kubeconfigPath,
		TunnelInfo:     tunnelInfo,
	}, nil
}
