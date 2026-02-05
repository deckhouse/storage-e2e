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

package config

import "time"

// Configuration parameters used in code

// DefaultSetupVM is the default VM configuration of the node that is used for bootstrap of test cluster.
// This VM is always created separately and should be deleted after cluster bootstrap.
var DefaultSetupVM = ClusterNode{
	Hostname:     "bootstrap-node-",
	HostType:     HostTypeVM,
	Role:         ClusterRoleSetup,
	OSType:       OSTypeMap["Ubuntu 22.04 6.2.0-39-generic"],
	CPU:          2,
	CoreFraction: func() *int { v := 50; return &v }(), // 50% core fraction
	RAM:          4,
	DiskSize:     20,
}

// Timeout constants for various operations during cluster creation and management
const (
	// VM operations
	VMCreationTimeout                   = 20 * time.Minute // Timeout for creating VMs (includes ClusterVirtualImage creation which can take up to 15 minutes)
	VMsRunningTimeout                   = 20 * time.Minute // Timeout for waiting for all VMs to become Running state
	VMInfoTimeout                       = 30 * time.Second // Timeout for gathering VM information
	ClusterVirtualImageReadinessTimeout = 15 * time.Minute // Timeout for waiting for ClusterVirtualImage to become provisioned (Ready)

	// Node operations
	NodesReadyTimeout = 15 * time.Minute // Timeout for waiting for nodes to become Ready

	// Cluster bootstrap and setup
	DKPDeployTimeout       = 30 * time.Minute // Timeout for DKP deployment (dhctl bootstrap)
	DockerInstallTimeout   = 10 * time.Minute // Timeout for Docker installation on setup node
	BootstrapUploadTimeout = 5 * time.Minute  // Timeout for uploading bootstrap files

	// Kubernetes operations
	ModuleCheckTimeout   = 10 * time.Second // Timeout for checking module status
	NamespaceTimeout     = 30 * time.Second // Timeout for creating namespace
	NodeGroupTimeout     = 3 * time.Second  // Timeout for creating NodeGroup
	SecretsWaitTimeout   = 2 * time.Minute  // Timeout for waiting for bootstrap secrets to appear
	ClusterHealthTimeout = 15 * time.Minute // Timeout for cluster health check
	ModuleDeployTimeout  = 15 * time.Minute // Timeout for waiting for ONE module to be ready

	// Test operations
	ClusterCreationTimeout = 90 * time.Minute // Total timeout for test cluster creation (includes module deployment)
	ClusterCleanupTimeout  = 10 * time.Minute // Timeout for cleaning up test cluster resources

	// Commander operations
	CommanderClusterReadyTimeout = 30 * time.Minute // Default timeout for waiting for Commander cluster to become ready

	// SSH operations - retry configuration for all SSH-related operations
	// This includes: connection establishment, command execution, tunnel creation, reconnection
	SSHRetryCount        = 10               // Number of retry attempts for SSH operations
	SSHRetryInitialDelay = 2 * time.Second  // Initial delay before first retry (doubles with each retry)
	SSHRetryMaxDelay     = 60 * time.Second // Maximum delay between retries
	SSHKeepaliveInterval = 60 * time.Second // Interval for SSH keepalive requests
)
