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

package commander

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// updateRevisionRetries bounds how many times an update is retried on an
// optimistic-locking (current_revision) conflict.
const updateRevisionRetries = 5

// UpdateCluster applies new template input values to an existing cluster via
// PUT /clusters/:id. The request carries current_revision for optimistic
// locking: a stale revision yields ErrRevisionConflict (409), and the caller
// re-fetches and retries.
func (c *Client) UpdateCluster(ctx context.Context, id string, req UpdateClusterRequest) (*ClusterResponse, error) {
	apiURL := fmt.Sprintf("%s%s/clusters/%s", c.baseURL, c.apiPrefix, id)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal update request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("%w: %s", ErrRevisionConflict, strings.TrimSpace(string(respBody)))
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(respBody))
	}

	var updated ClusterResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &updated); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
	}
	return &updated, nil
}

// UpdateClusterValues fetches the named cluster, applies mutate to a copy of its
// current template input values, and PUTs the result. It retries on a
// current_revision conflict by re-fetching and re-applying, so a concurrent
// Commander-side change does not lose the update.
func (c *Client) UpdateClusterValues(ctx context.Context, name string, mutate func(values map[string]interface{})) (*ClusterResponse, error) {
	var lastErr error
	for attempt := 0; attempt < updateRevisionRetries; attempt++ {
		cluster, err := c.GetClusterByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("get cluster %q: %w", name, err)
		}

		values := valuesToMap(cluster.Values)
		mutate(values)

		updated, err := c.UpdateCluster(ctx, cluster.ID, UpdateClusterRequest{
			CurrentRevision: cluster.CurrentRevision,
			Values:          values,
		})
		if err == nil {
			return updated, nil
		}
		lastErr = err
		if !errors.Is(err, ErrRevisionConflict) {
			return nil, err
		}
		// Revision conflict: re-fetch and retry with the fresh revision.
	}
	return nil, fmt.Errorf("update cluster %q values: exhausted %d revision-conflict retries: %w", name, updateRevisionRetries, lastErr)
}

// ListClusterChangeRequests returns the change requests for the given cluster
// (GET /cluster_change_requests?cluster_id=:id). A cluster resize that needs
// approval surfaces here as a pending change request.
func (c *Client) ListClusterChangeRequests(ctx context.Context, clusterID string) ([]ClusterChangeRequest, error) {
	apiURL := fmt.Sprintf("%s%s/cluster_change_requests?cluster_id=%s", c.baseURL, c.apiPrefix, url.QueryEscape(clusterID))

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

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// The API returns either a bare array or an items/data-wrapped object.
	var arr []ClusterChangeRequest
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, nil
	}
	var wrapped ClusterChangeRequestListResponse
	if err := json.Unmarshal(body, &wrapped); err == nil {
		if len(wrapped.Items) > 0 {
			return wrapped.Items, nil
		}
		return wrapped.Data, nil
	}
	return nil, fmt.Errorf("failed to decode change requests response: %s", string(body))
}

// ApproveClusterChangeRequest approves a pending change request
// (POST /cluster_change_requests/:id/approve). An already-approved or
// already-completed request is treated as success (the approval is idempotent);
// a cluster_cannot_be_converged response returns ErrClusterCannotConverge.
func (c *Client) ApproveClusterChangeRequest(ctx context.Context, id string) error {
	apiURL := fmt.Sprintf("%s%s/cluster_change_requests/%s/approve", c.baseURL, c.apiPrefix, url.PathEscape(id))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusConflict {
		lower := strings.ToLower(string(body))
		switch {
		case strings.Contains(lower, "already_approved"), strings.Contains(lower, "already_completed"):
			return nil // idempotent: nothing to do
		case strings.Contains(lower, "cluster_cannot_be_converged"):
			return fmt.Errorf("%w: change request %s: %s", ErrClusterCannotConverge, id, strings.TrimSpace(string(body)))
		}
	}
	return fmt.Errorf("approve change request %s: unexpected status %d: %s", id, resp.StatusCode, string(body))
}

// SetClusterInputValueAndWait sets a single template input value on the named
// cluster and waits for it to converge (status in_sync). A change that requires
// approval (e.g. a control-plane resize disruption) surfaces as a pending
// cluster_change_request; the wait loop approves any such requests for this
// cluster so the convergence can proceed. It returns when the cluster is in_sync
// or fails/timeouts.
func (c *Client) SetClusterInputValueAndWait(ctx context.Context, name, key string, value interface{}, timeout time.Duration) error {
	updated, err := c.UpdateClusterValues(ctx, name, func(values map[string]interface{}) {
		values[key] = value
	})
	if err != nil {
		return fmt.Errorf("set %q=%v on cluster %q: %w", key, value, name, err)
	}
	clusterID := updated.ID

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var lastStatus string
	for {
		// Approve any pending change requests so a disruptive resize can proceed.
		if clusterID != "" {
			if crs, crErr := c.ListClusterChangeRequests(ctx, clusterID); crErr == nil {
				for _, cr := range crs {
					if err := c.ApproveClusterChangeRequest(ctx, cr.ID); err != nil {
						if errors.Is(err, ErrClusterCannotConverge) {
							return err
						}
						// Otherwise transient/not-yet-approvable: retry next tick.
					}
				}
			}
		}

		cluster, err := c.GetClusterByName(ctx, name)
		if err == nil {
			lastStatus = cluster.Status
			if clusterID == "" {
				clusterID = cluster.ID
			}
			switch mapStatusToPhase(cluster.Status) {
			case ClusterPhaseReady:
				return nil
			case ClusterPhaseFailed:
				return fmt.Errorf("cluster %q failed to converge (status %q): %s", name, cluster.Status, cluster.Message)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timeout waiting for cluster %q to converge after setting %q=%v (last status: %s)", name, key, value, lastStatus)
		case <-ticker.C:
		}
	}
}

// valuesToMap coerces a cluster's Values (decoded as interface{}) into a
// mutable map. A nil or non-object value yields an empty map.
func valuesToMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		out := make(map[string]interface{}, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out
	}
	return map[string]interface{}{}
}
