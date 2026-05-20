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

package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// Well-known Rook resources that hold Ceph connection data.
const (
	// RookMonSecretName is the Secret that the Rook operator populates with
	// admin credentials and cluster fsid once the CephCluster is bootstrapped.
	RookMonSecretName = "rook-ceph-mon"

	// RookMonEndpointsConfigMapName is the ConfigMap the operator keeps in
	// sync with the current set of Ceph monitors.
	RookMonEndpointsConfigMapName = "rook-ceph-mon-endpoints"
)

// CephCredentials holds the information a Ceph CSI client needs to connect
// to a cluster bootstrapped by Rook.
type CephCredentials struct {
	// FSID is the Ceph cluster unique identifier.
	FSID string

	// AdminUser is the Ceph user name (typically "admin").
	AdminUser string

	// AdminKey is the CephX key for AdminUser.
	AdminKey string

	// Monitors is the list of monitor endpoints in "IP:PORT" form, sorted
	// alphabetically to make the output stable across runs.
	Monitors []string
}

// WaitForCephCredentials blocks until all pieces of information required to
// connect to the Rook-managed Ceph cluster are populated:
//   - Secret `rook-ceph-mon` exists and has `fsid`, `ceph-username`, `ceph-secret`.
//   - ConfigMap `rook-ceph-mon-endpoints` exists and has at least one reachable monitor.
//
// The returned CephCredentials is suitable for wiring csi-ceph CRs
// (CephClusterConnection, CephClusterAuthentication).
func WaitForCephCredentials(ctx context.Context, kubeconfig *rest.Config, namespace string, timeout time.Duration) (*CephCredentials, error) {
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	logger.Debug("Waiting for Ceph credentials in %s (timeout: %v)", namespace, timeout)

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, RookMonSecretName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Debug("Failed to get Secret %s/%s: %v", namespace, RookMonSecretName, err)
		}

		cm, cmErr := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, RookMonEndpointsConfigMapName, metav1.GetOptions{})
		if cmErr != nil && !apierrors.IsNotFound(cmErr) {
			logger.Debug("Failed to get ConfigMap %s/%s: %v", namespace, RookMonEndpointsConfigMapName, cmErr)
		}

		if err == nil && cmErr == nil {
			creds, extractErr := extractCephCredentials(secret.Data, cm.Data)
			if extractErr == nil {
				logger.Success("Ceph credentials ready in %s (fsid=%s, %d monitor(s))", namespace, creds.FSID, len(creds.Monitors))
				return creds, nil
			}
			logger.Debug("Rook credentials not complete yet: %v", extractErr)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for Ceph credentials in %s: %w", namespace, ctx.Err())
		case <-ticker.C:
		}
	}
}

// extractCephCredentials parses the Rook-managed Secret/ConfigMap payloads
// into a CephCredentials struct. It returns an error if any required field
// is missing so the caller can keep polling until the operator has populated
// everything.
func extractCephCredentials(secretData map[string][]byte, cmData map[string]string) (*CephCredentials, error) {
	fsid := strings.TrimSpace(string(secretData["fsid"]))
	if fsid == "" {
		return nil, fmt.Errorf("Secret %s is missing `fsid`", RookMonSecretName)
	}

	adminUser := strings.TrimSpace(string(secretData["ceph-username"]))
	if adminUser == "" {
		adminUser = "client.admin"
	}
	adminUser = strings.TrimPrefix(adminUser, "client.")

	adminKey := strings.TrimSpace(string(secretData["ceph-secret"]))
	if adminKey == "" {
		return nil, fmt.Errorf("Secret %s is missing `ceph-secret`", RookMonSecretName)
	}

	raw, ok := cmData["data"]
	if !ok {
		return nil, fmt.Errorf("ConfigMap %s is missing `data`", RookMonEndpointsConfigMapName)
	}
	monitors, err := parseMonEndpoints(raw)
	if err != nil {
		return nil, err
	}
	if len(monitors) == 0 {
		return nil, fmt.Errorf("ConfigMap %s has no populated monitor endpoints", RookMonEndpointsConfigMapName)
	}

	return &CephCredentials{
		FSID:      fsid,
		AdminUser: adminUser,
		AdminKey:  adminKey,
		Monitors:  monitors,
	}, nil
}

// parseMonEndpoints parses the Rook-maintained monitor endpoints string.
//
// Rook stores the current mon list in the `data` key of the
// `rook-ceph-mon-endpoints` ConfigMap as a comma-separated list of
// `<mon-name>=<ip>:<port>` pairs, for example:
//
//	a=10.0.0.1:6789,b=10.0.0.2:6789,c=10.0.0.3:6789
//
// This helper returns just the `<ip>:<port>` portion of every entry, sorted
// alphabetically for stable output.
func parseMonEndpoints(raw string) ([]string, error) {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Strip the "<mon-name>=" prefix if present.
		if idx := strings.Index(part, "="); idx >= 0 {
			part = part[idx+1:]
		}
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	sort.Strings(out)
	return out, nil
}
