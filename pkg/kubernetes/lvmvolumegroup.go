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

	"k8s.io/client-go/rest"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/storage"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// ThinPoolSpec represents a thin pool specification for LVMVolumeGroup
type ThinPoolSpec struct {
	Name            string // Thin pool name
	Size            string // Size of the thin pool (e.g., "50%" or "10Gi")
	AllocationLimit string // Allocation limit (optional)
}

// CreateLVMVolumeGroup creates an LVMVolumeGroup resource for a specific node
func CreateLVMVolumeGroup(ctx context.Context, kubeconfig *rest.Config, name, nodeName string, blockDeviceNames []string, actualVGName string) error {
	logger.Debug("Creating LVMVolumeGroup %s for node %s with %d block devices", name, nodeName, len(blockDeviceNames))

	lvgClient, err := storage.NewLVMVolumeGroupClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup client: %w", err)
	}

	if err := lvgClient.Create(ctx, name, nodeName, blockDeviceNames, actualVGName); err != nil {
		return err
	}

	logger.Success("LVMVolumeGroup %s created successfully", name)
	return nil
}

// CreateLVMVolumeGroupWithThinPool creates an LVMVolumeGroup resource with thin pools for a specific node
func CreateLVMVolumeGroupWithThinPool(ctx context.Context, kubeconfig *rest.Config, name, nodeName string, blockDeviceNames []string, actualVGName string, thinPools []ThinPoolSpec) error {
	logger.Debug("Creating LVMVolumeGroup %s for node %s with %d block devices and %d thin pools", name, nodeName, len(blockDeviceNames), len(thinPools))

	lvgClient, err := storage.NewLVMVolumeGroupClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup client: %w", err)
	}

	// Convert ThinPoolSpec to snc.LVMVolumeGroupThinPoolSpec
	sncThinPools := make([]snc.LVMVolumeGroupThinPoolSpec, len(thinPools))
	for i, tp := range thinPools {
		sncThinPools[i] = snc.LVMVolumeGroupThinPoolSpec{
			Name:            tp.Name,
			Size:            tp.Size,
			AllocationLimit: tp.AllocationLimit,
		}
	}

	if err := lvgClient.CreateWithThinPools(ctx, name, nodeName, blockDeviceNames, actualVGName, sncThinPools); err != nil {
		return err
	}

	logger.Success("LVMVolumeGroup %s with thin pools created successfully", name)
	return nil
}

// WaitForLVMVolumeGroupReady waits for an LVMVolumeGroup to become Ready
func WaitForLVMVolumeGroupReady(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	logger.Debug("Waiting for LVMVolumeGroup %s to become Ready (timeout: %v)", name, timeout)

	lvgClient, err := storage.NewLVMVolumeGroupClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup client: %w", err)
	}

	if err := lvgClient.WaitForReady(ctx, name, timeout); err != nil {
		return err
	}

	logger.Success("LVMVolumeGroup %s is Ready", name)
	return nil
}

// DeleteLVMVolumeGroup deletes an LVMVolumeGroup resource by name
func DeleteLVMVolumeGroup(ctx context.Context, kubeconfig *rest.Config, name string) error {
	logger.Debug("Deleting LVMVolumeGroup %s", name)

	lvgClient, err := storage.NewLVMVolumeGroupClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup client: %w", err)
	}

	if err := lvgClient.Delete(ctx, name); err != nil {
		return err
	}

	logger.Success("LVMVolumeGroup %s deleted", name)
	return nil
}

// WaitForLVMVolumeGroupDeletion waits for an LVMVolumeGroup to be deleted
func WaitForLVMVolumeGroupDeletion(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	logger.Debug("Waiting for LVMVolumeGroup %s to be deleted (timeout: %v)", name, timeout)

	lvgClient, err := storage.NewLVMVolumeGroupClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup client: %w", err)
	}

	if err := lvgClient.WaitForDeletion(ctx, name, timeout); err != nil {
		return err
	}

	logger.Success("LVMVolumeGroup %s is deleted", name)
	return nil
}
