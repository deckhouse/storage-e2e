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

package commander

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterPhase represents the phase of a cluster in Commander
type ClusterPhase string

const (
	// ClusterPhaseDraft indicates the cluster is in draft state
	ClusterPhaseDraft ClusterPhase = "Draft"
	// ClusterPhaseCreating indicates the cluster is being created
	ClusterPhaseCreating ClusterPhase = "Creating"
	// ClusterPhaseReadyToJoin indicates the cluster is ready to join
	ClusterPhaseReadyToJoin ClusterPhase = "ReadyToJoin"
	// ClusterPhaseJoining indicates the cluster is joining
	ClusterPhaseJoining ClusterPhase = "Joining"
	// ClusterPhaseReady indicates the cluster is ready
	ClusterPhaseReady ClusterPhase = "Ready"
	// ClusterPhaseUpdating indicates the cluster is being updated
	ClusterPhaseUpdating ClusterPhase = "Updating"
	// ClusterPhaseDeleting indicates the cluster is being deleted
	ClusterPhaseDeleting ClusterPhase = "Deleting"
	// ClusterPhaseFailed indicates the cluster creation/update failed
	ClusterPhaseFailed ClusterPhase = "Failed"
)

// Cluster represents a Deckhouse Commander cluster resource
// API Group: commander.deckhouse.io/v1alpha1
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// ClusterSpec defines the desired state of a cluster
type ClusterSpec struct {
	// TemplateName is the name of the template used to create this cluster
	TemplateName string `json:"templateName,omitempty"`

	// TemplateVersion is the version of the template
	TemplateVersion string `json:"templateVersion,omitempty"`

	// InputValues are the input parameters for the template
	InputValues map[string]interface{} `json:"inputValues,omitempty"`

	// SSHConfig contains SSH connection parameters for attached clusters
	SSHConfig *SSHConfig `json:"sshConfig,omitempty"`
}

// SSHConfig contains SSH connection parameters
type SSHConfig struct {
	// Host is the SSH host address
	Host string `json:"host"`

	// Port is the SSH port (default: 22)
	Port int `json:"port,omitempty"`

	// User is the SSH username
	User string `json:"user"`

	// PrivateKey is the SSH private key (base64 encoded)
	PrivateKey string `json:"privateKey,omitempty"`

	// PrivateKeySecretRef references a secret containing the private key
	PrivateKeySecretRef *SecretKeyRef `json:"privateKeySecretRef,omitempty"`
}

// SecretKeyRef is a reference to a secret key
type SecretKeyRef struct {
	// Name is the name of the secret
	Name string `json:"name"`

	// Namespace is the namespace of the secret
	Namespace string `json:"namespace,omitempty"`

	// Key is the key within the secret
	Key string `json:"key"`
}

// ClusterStatus defines the observed state of a cluster
type ClusterStatus struct {
	// Phase is the current phase of the cluster
	Phase ClusterPhase `json:"phase,omitempty"`

	// Message is a human-readable message about the cluster state
	Message string `json:"message,omitempty"`

	// KubeconfigSecretRef references a secret containing the kubeconfig
	KubeconfigSecretRef *SecretKeyRef `json:"kubeconfigSecretRef,omitempty"`

	// APIEndpoint is the API endpoint of the cluster
	APIEndpoint string `json:"apiEndpoint,omitempty"`

	// MasterNodes contains information about master nodes
	MasterNodes []NodeInfo `json:"masterNodes,omitempty"`

	// Conditions represent the latest available observations of the cluster's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NodeInfo contains information about a cluster node
type NodeInfo struct {
	// Name is the name of the node
	Name string `json:"name"`

	// IPAddress is the IP address of the node
	IPAddress string `json:"ipAddress,omitempty"`

	// Role is the role of the node (master, worker)
	Role string `json:"role,omitempty"`
}

// ClusterList contains a list of Cluster resources
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Cluster `json:"items"`
}

// Template represents a Deckhouse Commander template resource
type Template struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec TemplateSpec `json:"spec,omitempty"`
}

// TemplateSpec defines the desired state of a template
type TemplateSpec struct {
	// Description is a human-readable description of the template
	Description string `json:"description,omitempty"`

	// Versions contains the available versions of the template
	Versions []TemplateVersion `json:"versions,omitempty"`
}

// TemplateVersion represents a version of a template
type TemplateVersion struct {
	// Name is the version name (e.g., "v1.0.0")
	Name string `json:"name"`

	// InputSchema defines the input parameters schema
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// TemplateList contains a list of Template resources
type TemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Template `json:"items"`
}

// ClusterConnectionInfo contains information needed to connect to a cluster
type ClusterConnectionInfo struct {
	// Kubeconfig is the kubeconfig content for the cluster
	Kubeconfig string

	// APIEndpoint is the API endpoint of the cluster
	APIEndpoint string

	// SSHHost is the SSH host for connecting to the cluster
	SSHHost string

	// SSHUser is the SSH user for connecting to the cluster
	SSHUser string

	// SSHPort is the SSH port for connecting to the cluster
	SSHPort int
}

// CreateClusterRequest represents the request body for creating a cluster in Commander API
// See: https://deckhouse.io/modules/commander/stable/integration_api.html
type CreateClusterRequest struct {
	// Name is the name of the cluster
	Name string `json:"name"`

	// ClusterTemplateVersionID is the ID of the template version to use
	ClusterTemplateVersionID string `json:"cluster_template_version_id"`

	// RegistryID is the ID of the registry to use for the cluster (required by some templates)
	RegistryID string `json:"registry_id,omitempty"`

	// Values are the input parameters for the template (e.g., releaseChannel, kubeVersion, slot, etc.)
	Values map[string]interface{} `json:"values,omitempty"`
}

// ClusterTemplateResponse represents a cluster template from Commander API
type ClusterTemplateResponse struct {
	ID                              string                    `json:"id"`
	Name                            string                    `json:"name"`
	Comment                         string                    `json:"comment,omitempty"`
	CurrentRevision                 int                       `json:"current_revision,omitempty"`
	CurrentClusterTemplateVersionID string                    `json:"current_cluster_template_version_id,omitempty"`
	Immutable                       bool                      `json:"immutable,omitempty"`
	ClusterTemplateVersions         []TemplateVersionResponse `json:"cluster_template_versions,omitempty"`
	// Legacy field name for compatibility
	Versions []TemplateVersionResponse `json:"versions,omitempty"`
}

// TemplateVersionResponse represents a template version from Commander API
type TemplateVersionResponse struct {
	ID                string                 `json:"id"`
	ClusterTemplateID string                 `json:"cluster_template_id,omitempty"`
	Name              string                 `json:"name"`
	Comment           string                 `json:"comment,omitempty"`
	Version           string                 `json:"version,omitempty"`
	InputSchema       map[string]interface{} `json:"input_schema,omitempty"`
}

// ClusterTemplateListResponse represents the list of templates from Commander API
type ClusterTemplateListResponse struct {
	Items []ClusterTemplateResponse `json:"items,omitempty"`
	Data  []ClusterTemplateResponse `json:"data,omitempty"` // Some APIs return data instead of items
}

// ClusterResponse represents a cluster from Commander API
// Note: Many fields use interface{} because the API can return different types (object, array, null, string)
type ClusterResponse struct {
	ID                                                  string      `json:"id"`
	CurrentRevision                                     int         `json:"current_revision,omitempty"`
	Name                                                string      `json:"name"`
	WasCreated                                          bool        `json:"was_created,omitempty"`
	IsLocked                                            bool        `json:"is_locked,omitempty"`
	ClusterType                                         string      `json:"cluster_type,omitempty"`
	Status                                              string      `json:"status,omitempty"`
	ResourcesSyncState                                  string      `json:"resources_sync_state,omitempty"`
	ResourcesStateResults                               interface{} `json:"resources_state_results,omitempty"`
	ResourcesCheckedAt                                  string      `json:"resources_checked_at,omitempty"`
	ResourcesAppliedAt                                  string      `json:"resources_applied_at,omitempty"`
	Values                                              interface{} `json:"values,omitempty"`
	ClusterTemplateVersionID                            string      `json:"cluster_template_version_id,omitempty"`
	ClusterTemplateVersionSwitchedAt                    string      `json:"cluster_template_version_switched_at,omitempty"`
	ClusterConfigurationAppliedAt                       string      `json:"cluster_configuration_applied_at,omitempty"`
	ClusterConfigurationUpdatedAt                       string      `json:"cluster_configuration_updated_at,omitempty"`
	DhctlConfigurationRendered                          string      `json:"dhctl_configuration_rendered,omitempty"`
	AppliedClusterConfigurationRendered                 string      `json:"applied_cluster_configuration_rendered,omitempty"`
	AppliedProviderSpecificClusterConfigurationRendered string      `json:"applied_provider_specific_cluster_configuration_rendered,omitempty"`
	DesiredClusterConfigurationRendered                 string      `json:"desired_cluster_configuration_rendered,omitempty"`
	DesiredProviderSpecificClusterConfigurationRendered string      `json:"desired_provider_specific_cluster_configuration_rendered,omitempty"`
	RenderErrors                                        interface{} `json:"render_errors,omitempty"`
	RegistryID                                          string      `json:"registry_id,omitempty"`
	ClusterKubernetesResourceGroupVersionsRendered      interface{} `json:"cluster_kubernetes_resource_group_versions_rendered,omitempty"`
	ClusterAgentData                                    interface{} `json:"cluster_agent_data,omitempty"`
	ConnectionHosts                                     interface{} `json:"connection_hosts,omitempty"`
	CreatedAt                                           string      `json:"created_at,omitempty"`
	UpdatedAt                                           string      `json:"updated_at,omitempty"`
	ArchivedAt                                          string      `json:"archived_at,omitempty"`
	ArchiveNumber                                       string      `json:"archive_number,omitempty"`
	AgentAPIKey                                         interface{} `json:"agent_api_key,omitempty"`
	AgentStatus                                         interface{} `json:"agent_status,omitempty"`
	InitResourcesRendered                               interface{} `json:"init_resources_rendered,omitempty"`
	DesiredResourcesRendered                            interface{} `json:"desired_resources_rendered,omitempty"`
	AppliedResourcesRendered                            interface{} `json:"applied_resources_rendered,omitempty"`

	// Legacy/compatibility fields (may not be present in API response)
	Phase       string     `json:"phase,omitempty"`
	APIEndpoint string     `json:"api_endpoint,omitempty"`
	Kubeconfig  string     `json:"kubeconfig,omitempty"`
	MasterNodes []NodeInfo `json:"master_nodes,omitempty"`
	SSHHost     string     `json:"ssh_host,omitempty"`
	SSHUser     string     `json:"ssh_user,omitempty"`
	SSHPort     int        `json:"ssh_port,omitempty"`
	Message     string     `json:"message,omitempty"`
}

// ClusterAgentData represents the cluster agent data from Commander API
type ClusterAgentData struct {
	ID            string                 `json:"id,omitempty"`
	ClusterID     string                 `json:"cluster_id,omitempty"`
	Source        string                 `json:"source,omitempty"`
	Data          map[string]interface{} `json:"data,omitempty"`
	CreatedAt     string                 `json:"created_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	ArchivedAt    string                 `json:"archived_at,omitempty"`
	ArchiveNumber string                 `json:"archive_number,omitempty"`
}

// ClusterListResponse represents the list of clusters from Commander API
type ClusterListResponse struct {
	Items []ClusterResponse `json:"items,omitempty"`
	Data  []ClusterResponse `json:"data,omitempty"` // Some APIs return data instead of items
}

// RegistryResponse represents a registry from Commander API
type RegistryResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ImagesRepo string `json:"images_repo,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}
