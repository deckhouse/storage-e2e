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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

// CreateStaticNodeGroup creates a NodeGroup resource with Static nodeType.
//
// Right after bootstrap the node-manager validating webhook
// (node-controller-webhook in d8-cloud-instance-manager) is frequently not
// reachable yet, so the apiserver rejects the create with a transient
// InternalError ("failed calling webhook ... connect: operation not
// permitted"). We retry with backoff until the webhook converges;
// retry.IsRetryable already classifies both InternalError and
// "failed calling webhook" as transient. The loop is bounded by the caller's
// context (config.NodeGroupTimeout).
func CreateStaticNodeGroup(ctx context.Context, config *rest.Config, name string) error {
	retryCfg := retry.Config{
		MaxRetries:  30,
		InitialWait: 2 * time.Second,
		MaxWait:     15 * time.Second,
		Backoff:     1.5,
		LogRetries:  true,
	}

	return retry.DoVoid(ctx, retryCfg, fmt.Sprintf("create NodeGroup %s", name), func() error {
		// Check if NodeGroup already exists. A previous (retried) attempt may
		// have created it even though we never saw a success response, so this
		// keeps the operation idempotent across retries.
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
	})
}
