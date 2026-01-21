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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AuthMethod represents the authentication method for Commander API
type AuthMethod string

const (
	// AuthMethodBearer uses Authorization: Bearer <token>
	AuthMethodBearer AuthMethod = "bearer"
	// AuthMethodToken uses Authorization: Token <token>
	AuthMethodToken AuthMethod = "token"
	// AuthMethodXAuthToken uses X-Auth-Token: <token>
	AuthMethodXAuthToken AuthMethod = "x-auth-token"
	// AuthMethodCookie uses Cookie: token=<token>
	AuthMethodCookie AuthMethod = "cookie"
	// AuthMethodBasic uses Authorization: Basic <base64(user:token)>
	AuthMethodBasic AuthMethod = "basic"
)

// Client provides access to Deckhouse Commander API
type Client struct {
	baseURL    string
	apiPrefix  string // API path prefix (e.g., "/api/v1", "/api", "")
	token      string
	authMethod AuthMethod
	authUser   string // Used for basic auth
	httpClient *http.Client
}

// ClientOptions contains options for creating a Commander client
type ClientOptions struct {
	// InsecureSkipTLSVerify skips TLS certificate verification (for self-signed certificates)
	InsecureSkipTLSVerify bool

	// CACertPath is the path to a CA certificate file for verifying server certificates
	CACertPath string

	// AuthMethod specifies the authentication method (default: bearer)
	AuthMethod AuthMethod

	// AuthUser specifies the username for basic authentication
	AuthUser string

	// APIPrefix specifies the API path prefix (default: "/api/v1")
	// Common values: "/api/v1", "/api", ""
	APIPrefix string
}

// NewClient creates a new Commander client with default options
func NewClient(baseURL, token string) (*Client, error) {
	return NewClientWithOptions(baseURL, token, ClientOptions{})
}

// NewClientWithOptions creates a new Commander client with custom options
func NewClientWithOptions(baseURL, token string, opts ClientOptions) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL cannot be empty")
	}
	if token == "" {
		return nil, fmt.Errorf("token cannot be empty")
	}

	// Remove trailing slash from base URL
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Create TLS config based on options
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if opts.InsecureSkipTLSVerify {
		// Skip certificate verification (for self-signed certificates in test environments)
		tlsConfig.InsecureSkipVerify = true // #nosec G402 - This is intentional for testing environments
	} else if opts.CACertPath != "" {
		// Load custom CA certificate
		caCert, err := os.ReadFile(opts.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", opts.CACertPath, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", opts.CACertPath)
		}

		tlsConfig.RootCAs = caCertPool
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Set default auth method if not specified
	// Default is X-Auth-Token as per Commander documentation
	// See: https://deckhouse.io/modules/commander/stable/integration_api.html
	authMethod := opts.AuthMethod
	if authMethod == "" {
		authMethod = AuthMethodXAuthToken
	}

	// Set default API prefix if not specified
	apiPrefix := opts.APIPrefix
	if apiPrefix == "" {
		apiPrefix = "/api/v1"
	}
	// Remove trailing slash from API prefix
	apiPrefix = strings.TrimSuffix(apiPrefix, "/")

	return &Client{
		baseURL:    baseURL,
		apiPrefix:  apiPrefix,
		token:      token,
		authMethod: authMethod,
		authUser:   opts.AuthUser,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}, nil
}

// GetClusterByID retrieves a cluster by its ID
func (c *Client) GetClusterByID(ctx context.Context, id string) (*ClusterResponse, error) {
	apiURL := fmt.Sprintf("%s%s/clusters/%s", c.baseURL, c.apiPrefix, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrClusterNotFound
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var cluster ClusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&cluster); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &cluster, nil
}

// ListClustersAPI lists all clusters using Commander API format
func (c *Client) ListClustersAPI(ctx context.Context) ([]ClusterResponse, error) {
	apiURL := fmt.Sprintf("%s%s/clusters", c.baseURL, c.apiPrefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to decode as array first (API returns array directly)
	var clusters []ClusterResponse
	arrayErr := json.Unmarshal(body, &clusters)
	if arrayErr == nil {
		return clusters, nil
	}

	// Try to decode as object with items/data field
	var listResp ClusterListResponse
	objectErr := json.Unmarshal(body, &listResp)
	if objectErr == nil {
		// Return items or data depending on which is populated
		if len(listResp.Items) > 0 {
			return listResp.Items, nil
		}
		return listResp.Data, nil
	}

	// Both unmarshal attempts failed - show both errors for debugging
	// Also show a snippet of the response body for debugging
	bodyPreview := string(body)
	if len(bodyPreview) > 500 {
		bodyPreview = bodyPreview[:500] + "..."
	}
	return nil, fmt.Errorf("failed to decode response (array error: %v, object error: %v, body preview: %s)", arrayErr, objectErr, bodyPreview)
}

// GetClusterByName finds a cluster by name and returns its details
func (c *Client) GetClusterByName(ctx context.Context, name string) (*ClusterResponse, error) {
	clusters, err := c.ListClustersAPI(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, cl := range clusters {
		if cl.Name == name {
			return &cl, nil
		}
	}

	return nil, ErrClusterNotFound
}

// GetCluster retrieves a cluster by name (convenience method that calls GetClusterByName)
func (c *Client) GetCluster(ctx context.Context, name string) (*Cluster, error) {
	clusterResp, err := c.GetClusterByName(ctx, name)
	if err != nil {
		return nil, err
	}

	// Convert ClusterResponse to Cluster for compatibility
	cluster := &Cluster{
		Status: ClusterStatus{
			Phase:   mapStatusToPhase(clusterResp.Status),
			Message: clusterResp.Message,
		},
	}
	cluster.Name = clusterResp.Name

	// Try to extract API endpoint from connection_hosts if available
	if connHosts, ok := clusterResp.ConnectionHosts.(map[string]interface{}); ok && connHosts != nil {
		if apiEndpoint, ok := connHosts["api_endpoint"].(string); ok {
			cluster.Status.APIEndpoint = apiEndpoint
		}
	}

	return cluster, nil
}

// mapStatusToPhase maps Commander API status string to ClusterPhase
// See: https://deckhouse.io/modules/commander/stable/integration_api.html
// Known statuses: "new", "creating", "in_sync"
func mapStatusToPhase(status string) ClusterPhase {
	// Map known status values to ClusterPhase
	switch strings.ToLower(status) {
	case "in_sync", "insync", "ready", "running", "active":
		return ClusterPhaseReady
	case "new":
		return ClusterPhaseDraft
	case "creating", "provisioning", "bootstrapping":
		return ClusterPhaseCreating
	case "updating", "upgrading":
		return ClusterPhaseUpdating
	case "deleting", "terminating":
		return ClusterPhaseDeleting
	case "failed", "error":
		return ClusterPhaseFailed
	case "joining":
		return ClusterPhaseJoining
	case "ready_to_join", "readytojoin":
		return ClusterPhaseReadyToJoin
	default:
		// Return the status as-is if no mapping found
		return ClusterPhase(status)
	}
}

// ListClusters lists all clusters (legacy method, returns ClusterList)
func (c *Client) ListClusters(ctx context.Context) (*ClusterList, error) {
	clusters, err := c.ListClustersAPI(ctx)
	if err != nil {
		return nil, err
	}

	// Convert to ClusterList for compatibility
	clusterList := &ClusterList{
		Items: make([]Cluster, len(clusters)),
	}
	for i, cl := range clusters {
		clusterList.Items[i] = Cluster{
			Status: ClusterStatus{
				Phase:   mapStatusToPhase(cl.Status),
				Message: cl.Message,
			},
		}
		clusterList.Items[i].Name = cl.Name

		// Try to extract API endpoint from connection_hosts if available
		if connHosts, ok := cl.ConnectionHosts.(map[string]interface{}); ok && connHosts != nil {
			if apiEndpoint, ok := connHosts["api_endpoint"].(string); ok {
				clusterList.Items[i].Status.APIEndpoint = apiEndpoint
			}
		}
	}

	return clusterList, nil
}

// CreateCluster creates a new cluster from a template (legacy method, use CreateClusterFromTemplate instead)
func (c *Client) CreateCluster(ctx context.Context, cluster *Cluster) (*Cluster, error) {
	url := fmt.Sprintf("%s%s/clusters", c.baseURL, c.apiPrefix)

	body, err := json.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	var createdCluster Cluster
	if err := json.NewDecoder(resp.Body).Decode(&createdCluster); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &createdCluster, nil
}

// CreateClusterFromTemplate creates a new cluster using Commander API format
// See: https://deckhouse.io/modules/commander/stable/integration_api.html
func (c *Client) CreateClusterFromTemplate(ctx context.Context, name string, templateVersionID string, registryID string, values map[string]interface{}) (*ClusterResponse, error) {
	apiURL := fmt.Sprintf("%s%s/clusters", c.baseURL, c.apiPrefix)

	createReq := CreateClusterRequest{
		Name:                     name,
		ClusterTemplateVersionID: templateVersionID,
		RegistryID:               registryID,
		Values:                   values,
	}

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	var createdCluster ClusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&createdCluster); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &createdCluster, nil
}

// GetClusterTemplateByName retrieves a template by name and returns the Commander API format
func (c *Client) GetClusterTemplateByName(ctx context.Context, name string) (*ClusterTemplateResponse, error) {
	// First, list all templates and find by name
	templates, err := c.ListClusterTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}

	for _, t := range templates {
		if t.Name == name {
			return &t, nil
		}
	}

	return nil, ErrTemplateNotFound
}

// GetClusterTemplateVersions retrieves versions for a specific template by template ID
func (c *Client) GetClusterTemplateVersions(ctx context.Context, templateID string) ([]TemplateVersionResponse, error) {
	apiURL := fmt.Sprintf("%s%s/cluster_templates/%s/versions", c.baseURL, c.apiPrefix, templateID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to decode as array first
	var versions []TemplateVersionResponse
	if err := json.Unmarshal(body, &versions); err == nil {
		return versions, nil
	}

	// Try to decode as object with items/data field
	var listResp struct {
		Items []TemplateVersionResponse `json:"items,omitempty"`
		Data  []TemplateVersionResponse `json:"data,omitempty"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(listResp.Items) > 0 {
		return listResp.Items, nil
	}
	return listResp.Data, nil
}

// ListClusterTemplates lists all cluster templates in Commander API format
func (c *Client) ListClusterTemplates(ctx context.Context) ([]ClusterTemplateResponse, error) {
	apiURL := fmt.Sprintf("%s%s/cluster_templates", c.baseURL, c.apiPrefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to decode as array first (API returns array directly)
	var templates []ClusterTemplateResponse
	if err := json.Unmarshal(body, &templates); err == nil {
		return templates, nil
	}

	// Try to decode as object with items/data field
	var listResp ClusterTemplateListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Return items or data depending on which is populated
	if len(listResp.Items) > 0 {
		return listResp.Items, nil
	}
	return listResp.Data, nil
}

// DeleteClusterByID deletes a cluster by its ID
func (c *Client) DeleteClusterByID(ctx context.Context, id string) error {
	apiURL := fmt.Sprintf("%s%s/clusters/%s", c.baseURL, c.apiPrefix, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteCluster deletes a cluster by name (finds ID first, then deletes)
func (c *Client) DeleteCluster(ctx context.Context, name string) error {
	cluster, err := c.GetClusterByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find cluster '%s': %w", name, err)
	}

	return c.DeleteClusterByID(ctx, cluster.ID)
}

// GetClusterKubeconfigByID retrieves the kubeconfig for a cluster by its ID
// Note: This endpoint may not be available in all Commander versions
func (c *Client) GetClusterKubeconfigByID(ctx context.Context, id string) (string, error) {
	// Try /clusters/{id}/kubeconfig endpoint first
	apiURL := fmt.Sprintf("%s%s/clusters/%s/kubeconfig", c.baseURL, c.apiPrefix, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		// /kubeconfig endpoint may not exist - try to get kubeconfig from cluster details
		return c.getKubeconfigFromClusterDetails(ctx, id)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Check if response is JSON (might contain kubeconfig in a field) or raw kubeconfig
	bodyStr := string(body)
	if strings.HasPrefix(strings.TrimSpace(bodyStr), "{") {
		// Try to parse as JSON
		var kubeconfigResp struct {
			Kubeconfig string `json:"kubeconfig"`
			Data       string `json:"data"`
			Content    string `json:"content"`
		}
		if err := json.Unmarshal(body, &kubeconfigResp); err == nil {
			if kubeconfigResp.Kubeconfig != "" {
				return kubeconfigResp.Kubeconfig, nil
			}
			if kubeconfigResp.Data != "" {
				return kubeconfigResp.Data, nil
			}
			if kubeconfigResp.Content != "" {
				return kubeconfigResp.Content, nil
			}
		}
	}

	return bodyStr, nil
}

// getKubeconfigFromClusterDetails tries to get kubeconfig from the cluster details response
func (c *Client) getKubeconfigFromClusterDetails(ctx context.Context, id string) (string, error) {
	cluster, err := c.GetClusterByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster details: %w", err)
	}

	// Try to find kubeconfig in various fields
	// Check in ClusterAgentData
	if agentData, ok := cluster.ClusterAgentData.(map[string]interface{}); ok && agentData != nil {
		if data, ok := agentData["data"].(map[string]interface{}); ok && data != nil {
			if kubeconfig, ok := data["kubeconfig"].(string); ok && kubeconfig != "" {
				return kubeconfig, nil
			}
		}
		if kubeconfig, ok := agentData["kubeconfig"].(string); ok && kubeconfig != "" {
			return kubeconfig, nil
		}
	}

	// Check in ConnectionHosts
	if connHosts, ok := cluster.ConnectionHosts.(map[string]interface{}); ok && connHosts != nil {
		if kubeconfig, ok := connHosts["kubeconfig"].(string); ok && kubeconfig != "" {
			return kubeconfig, nil
		}
	}

	// Check in Values
	if values, ok := cluster.Values.(map[string]interface{}); ok && values != nil {
		if kubeconfig, ok := values["kubeconfig"].(string); ok && kubeconfig != "" {
			return kubeconfig, nil
		}
	}

	return "", fmt.Errorf("kubeconfig not found in cluster response (cluster id: %s, status: %s). "+
		"The /clusters/{id}/kubeconfig endpoint may not be available in this Commander version", id, cluster.Status)
}

// GetClusterKubeconfig retrieves the kubeconfig for a cluster by name
func (c *Client) GetClusterKubeconfig(ctx context.Context, name string) (string, error) {
	cluster, err := c.GetClusterByName(ctx, name)
	if err != nil {
		return "", err
	}

	return c.GetClusterKubeconfigByID(ctx, cluster.ID)
}

// GetClusterConnectionInfo retrieves connection information for a cluster by name
func (c *Client) GetClusterConnectionInfo(ctx context.Context, name string) (*ClusterConnectionInfo, error) {
	clusterResp, err := c.GetClusterByName(ctx, name)
	if err != nil {
		return nil, err
	}

	// Get kubeconfig (may fail if endpoint doesn't exist)
	kubeconfig, kubeconfigErr := c.GetClusterKubeconfigByID(ctx, clusterResp.ID)
	// Don't fail here - kubeconfig might be obtained via SSH later

	info := &ClusterConnectionInfo{
		Kubeconfig: kubeconfig,
	}

	// Try to extract connection info from connection_hosts
	// Format: {"masters": [{"host": "10.211.1.16", "user": "ubuntu"}], ...}
	if connHosts, ok := clusterResp.ConnectionHosts.(map[string]interface{}); ok && connHosts != nil {
		// Try to get api_endpoint
		if apiEndpoint, ok := connHosts["api_endpoint"].(string); ok {
			info.APIEndpoint = apiEndpoint
		}

		// Try to get first master from masters array
		if masters, ok := connHosts["masters"].([]interface{}); ok && len(masters) > 0 {
			if firstMaster, ok := masters[0].(map[string]interface{}); ok {
				if host, ok := firstMaster["host"].(string); ok {
					info.SSHHost = host
				}
				if user, ok := firstMaster["user"].(string); ok {
					info.SSHUser = user
				}
				if port, ok := firstMaster["port"].(float64); ok {
					info.SSHPort = int(port)
				}
			}
		}

		// Fallback to direct ssh_host/ssh_user fields
		if info.SSHHost == "" {
			if sshHost, ok := connHosts["ssh_host"].(string); ok {
				info.SSHHost = sshHost
			}
			if sshUser, ok := connHosts["ssh_user"].(string); ok {
				info.SSHUser = sshUser
			}
			if sshPort, ok := connHosts["ssh_port"].(float64); ok {
				info.SSHPort = int(sshPort)
			}
		}
	}

	// Try to extract connection info from cluster_agent_data if not found
	if info.SSHHost == "" {
		if agentData, ok := clusterResp.ClusterAgentData.(map[string]interface{}); ok && agentData != nil {
			if data, ok := agentData["data"].(map[string]interface{}); ok && data != nil {
				if sshHost, ok := data["ssh_host"].(string); ok {
					info.SSHHost = sshHost
				}
				if sshUser, ok := data["ssh_user"].(string); ok {
					info.SSHUser = sshUser
				}
				if sshPort, ok := data["ssh_port"].(float64); ok {
					info.SSHPort = int(sshPort)
				}
			}
		}
	}

	// Fallback to legacy fields if still not found
	if info.SSHHost == "" && clusterResp.SSHHost != "" {
		info.SSHHost = clusterResp.SSHHost
		info.SSHUser = clusterResp.SSHUser
		info.SSHPort = clusterResp.SSHPort
	}

	// Set default SSH port if not specified
	if info.SSHHost != "" && info.SSHPort == 0 {
		info.SSHPort = 22
	}

	// If kubeconfig retrieval failed but we have SSH info, that's okay - we can get it via SSH
	if kubeconfigErr != nil && info.SSHHost == "" {
		return nil, fmt.Errorf("failed to get kubeconfig and no SSH connection info available: %w", kubeconfigErr)
	}

	return info, nil
}

// WaitForClusterReady waits for a cluster to become ready
func (c *Client) WaitForClusterReady(ctx context.Context, name string, timeout time.Duration) (*Cluster, error) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	var lastStatus string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutTimer.C:
			return nil, fmt.Errorf("timeout waiting for cluster %s to become ready (last status: %s)", name, lastStatus)
		case <-ticker.C:
			clusterResp, err := c.GetClusterByName(ctx, name)
			if err != nil {
				continue // Retry on error
			}

			lastStatus = clusterResp.Status
			phase := mapStatusToPhase(clusterResp.Status)

			switch phase {
			case ClusterPhaseReady:
				// Convert to Cluster and return
				cluster := &Cluster{
					Status: ClusterStatus{
						Phase:   phase,
						Message: clusterResp.Message,
					},
				}
				cluster.Name = clusterResp.Name
				return cluster, nil
			case ClusterPhaseFailed:
				return nil, fmt.Errorf("cluster %s failed (status: %s): %s", name, clusterResp.Status, clusterResp.Message)
			}
			// Continue waiting for other phases
		}
	}
}

// GetTemplate retrieves a template by name
func (c *Client) GetTemplate(ctx context.Context, name string) (*Template, error) {
	// URL encode the template name to handle spaces and special characters
	encodedName := url.PathEscape(name)
	templateURL := fmt.Sprintf("%s%s/cluster_templates/%s", c.baseURL, c.apiPrefix, encodedName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, templateURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrTemplateNotFound
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var template Template
	if err := json.NewDecoder(resp.Body).Decode(&template); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &template, nil
}

// ListTemplates lists all templates
func (c *Client) ListTemplates(ctx context.Context) (*TemplateList, error) {
	url := fmt.Sprintf("%s%s/cluster_templates", c.baseURL, c.apiPrefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var templates TemplateList
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &templates, nil
}

// ListRegistries lists all registries in Commander
func (c *Client) ListRegistries(ctx context.Context) ([]RegistryResponse, error) {
	apiURL := fmt.Sprintf("%s%s/registries", c.baseURL, c.apiPrefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to decode as array first (API returns array directly)
	var registries []RegistryResponse
	if err := json.Unmarshal(body, &registries); err == nil {
		return registries, nil
	}

	// Try to decode as object with items/data field
	var listResp struct {
		Items []RegistryResponse `json:"items,omitempty"`
		Data  []RegistryResponse `json:"data,omitempty"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Return items or data depending on which is populated
	if len(listResp.Items) > 0 {
		return listResp.Items, nil
	}
	return listResp.Data, nil
}

// GetRegistryByName finds a registry by name and returns its details
func (c *Client) GetRegistryByName(ctx context.Context, name string) (*RegistryResponse, error) {
	registries, err := c.ListRegistries(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list registries: %w", err)
	}

	for _, r := range registries {
		if r.Name == name {
			return &r, nil
		}
	}

	// If not found by exact name, try partial match
	for _, r := range registries {
		if strings.Contains(r.Name, name) {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("registry '%s' not found", name)
}

// setAuthHeaders sets the authorization headers for a request based on the configured auth method
func (c *Client) setAuthHeaders(req *http.Request) {
	switch c.authMethod {
	case AuthMethodToken:
		req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.token))
	case AuthMethodXAuthToken:
		req.Header.Set("X-Auth-Token", c.token)
	case AuthMethodCookie:
		req.AddCookie(&http.Cookie{
			Name:  "token",
			Value: c.token,
		})
	case AuthMethodBasic:
		// Basic auth: base64(user:token)
		auth := c.authUser + ":" + c.token
		encoded := base64Encode(auth)
		req.Header.Set("Authorization", fmt.Sprintf("Basic %s", encoded))
	case AuthMethodBearer:
		fallthrough
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	}
	req.Header.Set("Accept", "application/json")
}

// base64Encode encodes a string to base64
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
