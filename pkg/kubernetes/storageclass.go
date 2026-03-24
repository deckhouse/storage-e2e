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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
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

// GetDefaultStorageClassName returns the name of the current default StorageClass
// (annotated with storageclass.kubernetes.io/is-default-class=true), or "" if none exists.
func GetDefaultStorageClassName(ctx context.Context, kubeconfig *rest.Config) (string, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to create clientset: %w", err)
	}

	scList, err := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list StorageClasses: %w", err)
	}

	for _, sc := range scList.Items {
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sc.Name, nil
		}
	}
	return "", nil
}

// StorageClassExists returns true if a StorageClass with the given name exists.
func StorageClassExists(ctx context.Context, kubeconfig *rest.Config, name string) (bool, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to create clientset: %w", err)
	}

	_, err = clientset.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get StorageClass %s: %w", name, err)
	}
	return true, nil
}

// SetGlobalDefaultStorageClass updates the "global" ModuleConfig to set
// spec.settings.storageClass to the given name, making it the cluster default.
func SetGlobalDefaultStorageClass(ctx context.Context, kubeconfig *rest.Config, storageClassName string) error {
	const moduleName = "global"
	const moduleVersion = 1

	settings := map[string]interface{}{
		"storageClass": storageClassName,
	}

	mc, err := deckhouse.GetModuleConfig(ctx, kubeconfig, moduleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Creating global ModuleConfig with storageClass=%s", storageClassName)
			return deckhouse.CreateModuleConfig(ctx, kubeconfig, moduleName, moduleVersion, true, settings)
		}
		return fmt.Errorf("failed to get global ModuleConfig: %w", err)
	}

	existingSettings := map[string]interface{}{}
	if mc.Spec.Settings != nil {
		for k, v := range mc.Spec.Settings {
			existingSettings[k] = v
		}
	}
	existingSettings["storageClass"] = storageClassName

	logger.Info("Updating global ModuleConfig with storageClass=%s", storageClassName)
	enabled := true
	if mc.Spec.Enabled != nil {
		enabled = *mc.Spec.Enabled
	}
	return deckhouse.UpdateModuleConfig(ctx, kubeconfig, moduleName, mc.Spec.Version, enabled, existingSettings)
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
