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

	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// CreateStaticNodeGroup creates a NodeGroup resource with Static nodeType
func CreateStaticNodeGroup(ctx context.Context, config *rest.Config, name string) error {
	// Check if NodeGroup already exists
	_, err := deckhouse.GetNodeGroup(ctx, config, name)
	if err == nil {
		// NodeGroup already exists, nothing to do
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check if nodegroup %s exists: %w", name, err)
	}

	// Create NodeGroup with Static nodeType
	if err := deckhouse.CreateNodeGroup(ctx, config, name, "Static"); err != nil {
		return fmt.Errorf("failed to create nodegroup %s: %w", name, err)
	}

	return nil
}
