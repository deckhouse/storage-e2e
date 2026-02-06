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

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/pkg/retry"
)

// NewClientsetWithRetry creates a new Kubernetes clientset with retry logic
// for transient network errors. While kubernetes.NewForConfig itself does not
// make network calls, this wrapper provides a centralized factory with retry
// that validates the connection by performing a lightweight server version check.
// This ensures the cluster is reachable before returning the clientset.
func NewClientsetWithRetry(ctx context.Context, config *rest.Config) (*kubernetes.Clientset, error) {
	return retry.Do(ctx, retry.DefaultConfig, "create kubernetes clientset", func() (*kubernetes.Clientset, error) {
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
		}

		// Validate the connection by performing a lightweight API call.
		// This catches TLS handshake timeouts, connection refused, and other
		// transient network errors early, allowing the retry logic to handle them.
		_, err = clientset.Discovery().ServerVersion()
		if err != nil {
			return nil, fmt.Errorf("failed to verify cluster connectivity: %w", err)
		}

		return clientset, nil
	})
}

// NewDynamicClientWithRetry creates a new Kubernetes dynamic client with retry logic
// for transient network errors. Similar to NewClientsetWithRetry, this provides
// a centralized factory for dynamic clients with built-in retry.
func NewDynamicClientWithRetry(ctx context.Context, config *rest.Config) (dynamic.Interface, error) {
	return retry.Do(ctx, retry.DefaultConfig, "create dynamic client", func() (dynamic.Interface, error) {
		client, err := dynamic.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create dynamic client: %w", err)
		}
		return client, nil
	})
}
