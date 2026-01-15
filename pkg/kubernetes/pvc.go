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
	"encoding/json"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// WaitForPVCsBound waits for PVCs matching the label selector to be in Bound state
func WaitForPVCsBound(ctx context.Context, clientset *kubernetes.Clientset, namespace, labelSelector string, expectedCount int, maxAttempts int, interval time.Duration) error {
	attempt := 0
	lastLogTime := time.Now()

	for {
		pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return err
		}

		boundCount := 0
		pendingPVCs := []string{}
		for _, pvc := range pvcs.Items {
			if pvc.Status.Phase == corev1.ClaimBound {
				boundCount++
			} else {
				pendingPVCs = append(pendingPVCs, pvc.Name)
			}
		}

		if boundCount >= expectedCount {
			logger.Progress("All PVCs bound: %d/%d", boundCount, expectedCount)
			return nil
		}

		// Log progress every 30 seconds
		if time.Since(lastLogTime) >= 30*time.Second {
			logger.Progress("Waiting for PVCs to be bound: %d/%d (attempt %d)", boundCount, expectedCount, attempt)
			if len(pendingPVCs) > 0 && len(pendingPVCs) <= 10 {
				logger.Debug("Pending PVCs: %v", pendingPVCs)
			} else if len(pendingPVCs) > 10 {
				logger.Debug("Pending PVCs: %d PVCs still pending", len(pendingPVCs))
			}
			lastLogTime = time.Now()
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

// WaitForPVCsResized waits for PVCs to be resized to the target size
func WaitForPVCsResized(ctx context.Context, clientset *kubernetes.Clientset, namespace string, pvcNames []string, targetSize string, maxAttempts int, interval time.Duration) error {
	attempt := 0
	lastLogTime := time.Now()

	targetQuantity, err := resource.ParseQuantity(targetSize)
	if err != nil {
		return fmt.Errorf("invalid target size %s: %w", targetSize, err)
	}

	for {
		resizedCount := 0
		pendingPVCs := []string{}

		for _, name := range pvcNames {
			pvc, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			if pvc.Status.Capacity != nil {
				if currentSize, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
					if currentSize.Equal(targetQuantity) {
						resizedCount++
					} else {
						pendingPVCs = append(pendingPVCs, fmt.Sprintf("%s (%s->%s)", name, currentSize.String(), targetSize))
					}
				}
			} else {
				pendingPVCs = append(pendingPVCs, fmt.Sprintf("%s (no capacity)", name))
			}
		}

		if resizedCount == len(pvcNames) {
			logger.Progress("All PVCs resized: %d/%d to %s", resizedCount, len(pvcNames), targetSize)
			return nil
		}

		// Log progress every 30 seconds
		if time.Since(lastLogTime) >= 30*time.Second {
			logger.Progress("Waiting for PVCs to be resized to %s: %d/%d (attempt %d)", targetSize, resizedCount, len(pvcNames), attempt)
			if len(pendingPVCs) > 0 && len(pendingPVCs) <= 10 {
				logger.Debug("Pending PVCs: %v", pendingPVCs)
			} else if len(pendingPVCs) > 10 {
				logger.Debug("Pending PVCs: %d PVCs still resizing", len(pendingPVCs))
			}
			lastLogTime = time.Now()
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

// ResizeList resizes multiple PVCs to a new size in parallel
func ResizeList(ctx context.Context, clientset *kubernetes.Clientset, namespace string, pvcNames []string, newSize string) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(pvcNames))

	for _, name := range pvcNames {
		wg.Add(1)
		go func(pvcName string) {
			defer wg.Done()
			patch := []map[string]interface{}{
				{
					"op":    "replace",
					"path":  "/spec/resources/requests/storage",
					"value": newSize,
				},
			}
			patchBytes, err := json.Marshal(patch)
			if err != nil {
				errChan <- fmt.Errorf("failed to marshal patch: %w", err)
				return
			}
			_, err = clientset.CoreV1().PersistentVolumeClaims(namespace).Patch(
				ctx,
				pvcName,
				types.JSONPatchType,
				patchBytes,
				metav1.PatchOptions{},
			)
			if err != nil {
				errChan <- fmt.Errorf("failed to resize PVC %s: %w", pvcName, err)
			}
		}(name)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}
