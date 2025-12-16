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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/core"
)

// CreateNamespaceIfNotExists creates a namespace if it doesn't exist, or returns the existing one.
// This is a high-level function that uses the low-level core namespace client.
func CreateNamespaceIfNotExists(ctx context.Context, config *rest.Config, name string) (*corev1.Namespace, error) {
	nsClient, err := core.NewNamespaceClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace client: %w", err)
	}

	// Try to get the namespace to check if it exists
	ns, err := nsClient.Get(ctx, name)
	if err != nil {
		// If namespace doesn't exist, create it
		if apierrors.IsNotFound(err) {
			return nsClient.Create(ctx, name)
		}
		// For other errors, return them
		return nil, fmt.Errorf("failed to get namespace %s: %w", name, err)
	}

	// Namespace exists, return it
	return ns, nil
}
