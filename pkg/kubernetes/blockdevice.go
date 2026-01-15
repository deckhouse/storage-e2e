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

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/storage"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// BlockDevice represents a block device in the cluster (re-export for public API)
type BlockDevice = storage.BlockDeviceInfo

// GetConsumableBlockDevices returns all consumable BlockDevices from the cluster
func GetConsumableBlockDevices(ctx context.Context, kubeconfig *rest.Config) ([]BlockDevice, error) {
	logger.Debug("Getting consumable BlockDevices from cluster")

	bdClient, err := storage.NewBlockDeviceClient(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create BlockDevice client: %w", err)
	}

	blockDevices, err := bdClient.ListConsumable(ctx)
	if err != nil {
		return nil, err
	}

	logger.Debug("Found %d consumable BlockDevices", len(blockDevices))
	return blockDevices, nil
}
