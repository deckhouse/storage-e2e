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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// PVCClient provides operations on PersistentVolumeClaim resources
type PVCClient struct {
	client kubernetes.Interface
}

// NewPVCClient creates a new PVC client from a rest.Config
func NewPVCClient(config *rest.Config) (*PVCClient, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return &PVCClient{client: clientset}, nil
}

// Create creates a new PVC
func (c *PVCClient) Create(ctx context.Context, namespace string, pvc *corev1.PersistentVolumeClaim) (*corev1.PersistentVolumeClaim, error) {
	created, err := c.client.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create PVC %s/%s: %w", namespace, pvc.Name, err)
	}
	return created, nil
}

// Get retrieves a PVC by namespace and name
func (c *PVCClient) Get(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	pvc, err := c.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PVC %s/%s: %w", namespace, name, err)
	}
	return pvc, nil
}

// ListByLabelSelector lists PVCs in a namespace matching the label selector
func (c *PVCClient) ListByLabelSelector(ctx context.Context, namespace, labelSelector string) (*corev1.PersistentVolumeClaimList, error) {
	pvcs, err := c.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list PVCs in namespace %s with selector %s: %w", namespace, labelSelector, err)
	}
	return pvcs, nil
}

// Resize resizes a PVC to a new size
func (c *PVCClient) Resize(ctx context.Context, namespace, name, newSize string) error {
	patch := []map[string]interface{}{
		{
			"op":    "replace",
			"path":  "/spec/resources/requests/storage",
			"value": newSize,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	_, err = c.client.CoreV1().PersistentVolumeClaims(namespace).Patch(
		ctx,
		name,
		types.JSONPatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to resize PVC %s/%s: %w", namespace, name, err)
	}
	return nil
}

// ResizeList resizes multiple PVCs to a new size
func (c *PVCClient) ResizeList(ctx context.Context, namespace string, pvcNames []string, newSize string) error {
	for _, name := range pvcNames {
		if err := c.Resize(ctx, namespace, name, newSize); err != nil {
			return fmt.Errorf("failed to resize PVC %s: %w", name, err)
		}
	}
	return nil
}

// WaitForBound waits for PVCs matching the label selector to be in Bound state
func (c *PVCClient) WaitForBound(ctx context.Context, namespace, labelSelector string, expectedCount int, maxAttempts int, interval time.Duration) error {
	attempt := 0
	for {
		pvcs, err := c.ListByLabelSelector(ctx, namespace, labelSelector)
		if err != nil {
			return err
		}

		boundCount := 0
		for _, pvc := range pvcs.Items {
			if pvc.Status.Phase == corev1.ClaimBound {
				boundCount++
			}
		}

		if boundCount >= expectedCount {
			return nil
		}

		if boundCount > 0 {
			attempt++
		}

		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for PVCs to be bound: %d/%d bound after %d attempts", boundCount, expectedCount, maxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// WaitForResize waits for PVCs to be resized to the target size
func (c *PVCClient) WaitForResize(ctx context.Context, namespace string, pvcNames []string, targetSize string, maxAttempts int, interval time.Duration) error {
	attempt := 0
	targetQuantity, err := resource.ParseQuantity(targetSize)
	if err != nil {
		return fmt.Errorf("invalid target size %s: %w", targetSize, err)
	}

	for {
		resizedCount := 0
		for _, name := range pvcNames {
			pvc, err := c.Get(ctx, namespace, name)
			if err != nil {
				return err
			}

			if pvc.Status.Capacity != nil {
				if currentSize, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
					if currentSize.Equal(targetQuantity) {
						resizedCount++
					}
				}
			}
		}

		if resizedCount == len(pvcNames) {
			return nil
		}

		if resizedCount > 0 {
			attempt++
		}

		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for PVCs to be resized: %d/%d resized after %d attempts", resizedCount, len(pvcNames), maxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Delete deletes a PVC
func (c *PVCClient) Delete(ctx context.Context, namespace, name string) error {
	err := c.client.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete PVC %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteByLabelSelector deletes all PVCs matching the label selector
func (c *PVCClient) DeleteByLabelSelector(ctx context.Context, namespace, labelSelector string) error {
	return c.client.CoreV1().PersistentVolumeClaims(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

// WaitForDeletion waits for PVCs matching the label selector to be deleted
func (c *PVCClient) WaitForDeletion(ctx context.Context, namespace, labelSelector string, maxAttempts int, interval time.Duration) error {
	attempt := 0
	for {
		pvcs, err := c.ListByLabelSelector(ctx, namespace, labelSelector)
		if err != nil {
			// If listing fails, assume PVCs are deleted
			return nil
		}

		if len(pvcs.Items) == 0 {
			return nil
		}

		attempt++
		if maxAttempts > 0 && attempt >= maxAttempts {
			return fmt.Errorf("timeout waiting for PVCs to be deleted: %d remaining after %d attempts", len(pvcs.Items), maxAttempts)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
