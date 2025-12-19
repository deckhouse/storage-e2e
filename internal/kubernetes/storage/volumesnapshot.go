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

package storage

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var (
	// VolumeSnapshotGVR is the GroupVersionResource for VolumeSnapshot
	VolumeSnapshotGVR = schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshots",
	}
)

// VolumeSnapshotClient provides operations on VolumeSnapshot resources
type VolumeSnapshotClient struct {
	client dynamic.Interface
}

// NewVolumeSnapshotClient creates a new VolumeSnapshot client from a rest.Config
func NewVolumeSnapshotClient(config *rest.Config) (*VolumeSnapshotClient, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	return &VolumeSnapshotClient{client: client}, nil
}

// Create creates a new VolumeSnapshot
func (c *VolumeSnapshotClient) Create(ctx context.Context, namespace string, snapshot *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	created, err := c.client.Resource(VolumeSnapshotGVR).Namespace(namespace).Create(ctx, snapshot, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create VolumeSnapshot %s/%s: %w", namespace, snapshot.GetName(), err)
	}
	return created, nil
}

// Get retrieves a VolumeSnapshot by namespace and name
func (c *VolumeSnapshotClient) Get(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	snapshot, err := c.client.Resource(VolumeSnapshotGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get VolumeSnapshot %s/%s: %w", namespace, name, err)
	}
	return snapshot, nil
}

// ListByLabelSelector lists VolumeSnapshots in a namespace matching the label selector
func (c *VolumeSnapshotClient) ListByLabelSelector(ctx context.Context, namespace, labelSelector string) (*unstructured.UnstructuredList, error) {
	snapshots, err := c.client.Resource(VolumeSnapshotGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list VolumeSnapshots in namespace %s with selector %s: %w", namespace, labelSelector, err)
	}
	return snapshots, nil
}

// IsReady checks if a VolumeSnapshot is ready to use
func (c *VolumeSnapshotClient) IsReady(snapshot *unstructured.Unstructured) bool {
	status, found, err := unstructured.NestedMap(snapshot.Object, "status")
	if !found || err != nil {
		return false
	}
	readyToUse, found, err := unstructured.NestedBool(status, "readyToUse")
	if !found || err != nil {
		return false
	}
	return readyToUse
}

// WaitForReady waits for VolumeSnapshots matching the label selector to be ready
func (c *VolumeSnapshotClient) WaitForReady(ctx context.Context, namespace, labelSelector string, expectedCount int, maxAttempts int, interval time.Duration) error {
	attempt := 0
	for {
		snapshots, err := c.ListByLabelSelector(ctx, namespace, labelSelector)
		if err != nil {
			return err
		}

		readyCount := 0
		for _, snapshot := range snapshots.Items {
			if c.IsReady(&snapshot) {
				readyCount++
			}
		}

		if readyCount >= expectedCount {
			return nil
		}

		if readyCount > 0 {
			attempt++
		}

		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for VolumeSnapshots to be ready: %d/%d ready after %d attempts", readyCount, expectedCount, maxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Delete deletes a VolumeSnapshot
func (c *VolumeSnapshotClient) Delete(ctx context.Context, namespace, name string) error {
	err := c.client.Resource(VolumeSnapshotGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete VolumeSnapshot %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteByLabelSelector deletes all VolumeSnapshots matching the label selector
func (c *VolumeSnapshotClient) DeleteByLabelSelector(ctx context.Context, namespace, labelSelector string) error {
	snapshots, err := c.ListByLabelSelector(ctx, namespace, labelSelector)
	if err != nil {
		return err
	}

	for _, snapshot := range snapshots.Items {
		if err := c.Delete(ctx, namespace, snapshot.GetName()); err != nil {
			return fmt.Errorf("failed to delete VolumeSnapshot %s: %w", snapshot.GetName(), err)
		}
	}
	return nil
}

// WaitForDeletion waits for VolumeSnapshots matching the label selector to be deleted
func (c *VolumeSnapshotClient) WaitForDeletion(ctx context.Context, namespace, labelSelector string, maxAttempts int, interval time.Duration) error {
	attempt := 0
	for {
		snapshots, err := c.ListByLabelSelector(ctx, namespace, labelSelector)
		if err != nil {
			// If listing fails, assume snapshots are deleted
			return nil
		}

		if len(snapshots.Items) == 0 {
			return nil
		}

		attempt++
		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for VolumeSnapshots to be deleted: %d remaining after %d attempts", len(snapshots.Items), maxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
