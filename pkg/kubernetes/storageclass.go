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
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// WaitForStorageClasses waits for multiple storage classes to become available in parallel
// Returns map of storage class names to errors (nil if successful, error if failed/not found)
func WaitForStorageClasses(ctx context.Context, kubeconfig *rest.Config, storageClassNames []string, timeout time.Duration) map[string]error {
	logger.Debug("Waiting for %d StorageClasses to become available (timeout: %v)", len(storageClassNames), timeout)

	results := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, scName := range storageClassNames {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			err := WaitForStorageClass(ctx, kubeconfig, name, timeout)
			mu.Lock()
			results[name] = err
			mu.Unlock()
		}(scName)
	}

	wg.Wait()
	return results
}

// WaitForStorageClass waits for a storage class to become available
func WaitForStorageClass(ctx context.Context, kubeconfig *rest.Config, storageClassName string, timeout time.Duration) error {
	logger.Debug("Waiting for StorageClass %s to become available (timeout: %v)", storageClassName, timeout)

	// Create clientset from kubeconfig with retry for transient network errors
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		// Check if context is done
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if timeout reached
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for StorageClass %s", storageClassName)
		}

		// Try to get the storage class
		_, err := clientset.StorageV1().StorageClasses().Get(ctx, storageClassName, metav1.GetOptions{})
		if err == nil {
			logger.Success("StorageClass %s is available", storageClassName)
			return nil
		}

		// Wait a bit before retrying
		time.Sleep(5 * time.Second)
	}
}

// WaitForStorageClassDeletion waits for a storage class to be deleted
func WaitForStorageClassDeletion(ctx context.Context, kubeconfig *rest.Config, storageClassName string, timeout time.Duration) error {
	logger.Debug("Waiting for StorageClass %s to be deleted (timeout: %v)", storageClassName, timeout)

	// Create clientset from kubeconfig with retry for transient network errors
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		// Check if context is done
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if timeout reached
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for StorageClass %s to be deleted", storageClassName)
		}

		// Try to get the storage class
		_, err := clientset.StorageV1().StorageClasses().Get(ctx, storageClassName, metav1.GetOptions{})
		if err != nil {
			// Assume deleted if we can't get it
			logger.Success("StorageClass %s is deleted", storageClassName)
			return nil
		}

		// Wait a bit before retrying
		time.Sleep(2 * time.Second)
	}
}
