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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/storage"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// BlockDeviceGVR is the GroupVersionResource of the sds-node-configurator
// BlockDevice CR (cluster-scoped). Used to label individual BlockDevices so a
// selector (e.g. ElasticCluster.spec.storage.blockDeviceSelector) can adopt
// them for OSDs.
var BlockDeviceGVR = schema.GroupVersionResource{
	Group:    "storage.deckhouse.io",
	Version:  "v1alpha1",
	Resource: "blockdevices",
}

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

// GetConsumableBlockDevicesByNode returns consumable BlockDevices for a specific node.
func GetConsumableBlockDevicesByNode(ctx context.Context, kubeconfig *rest.Config, nodeName string) ([]BlockDevice, error) {
	if nodeName == "" {
		return nil, fmt.Errorf("nodeName is required")
	}

	logger.Debug("Getting consumable BlockDevices from node %s", nodeName)

	bdClient, err := storage.NewBlockDeviceClient(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create BlockDevice client: %w", err)
	}

	blockDevices, err := bdClient.ListConsumableByNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	logger.Debug("Found %d consumable BlockDevices on node %s", len(blockDevices), nodeName)
	return blockDevices, nil
}

// LabelBlockDevice sets a label on a single BlockDevice CR. Idempotent (skips
// the update when the label already has the desired value) and tolerant of
// optimistic-concurrency conflicts. Used to mark BlockDevices eligible for
// adoption by an ElasticCluster's blockDeviceSelector.
func LabelBlockDevice(ctx context.Context, kubeconfig *rest.Config, name, labelKey, labelValue string) error {
	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	const maxRetries = 5
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		bd, err := dynamicClient.Resource(BlockDeviceGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get BlockDevice %s: %w", name, err)
		}
		labels := bd.GetLabels()
		if labels[labelKey] == labelValue {
			logger.Debug("BlockDevice %s already has label %s=%s", name, labelKey, labelValue)
			return nil
		}
		if labels == nil {
			labels = map[string]string{}
		}
		labels[labelKey] = labelValue
		bd.SetLabels(labels)

		_, lastErr = dynamicClient.Resource(BlockDeviceGVR).Update(ctx, bd, metav1.UpdateOptions{})
		if lastErr == nil {
			logger.Info("Labeled BlockDevice %s with %s=%s", name, labelKey, labelValue)
			return nil
		}
		if apierrors.IsConflict(lastErr) {
			logger.Debug("Conflict labeling BlockDevice %s (attempt %d/%d), retrying...", name, attempt+1, maxRetries)
			continue
		}
		return fmt.Errorf("failed to label BlockDevice %s: %w", name, lastErr)
	}
	return fmt.Errorf("failed to label BlockDevice %s after %d attempts: %w", name, maxRetries, lastErr)
}
