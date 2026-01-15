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
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	k8sapply "github.com/deckhouse/storage-e2e/internal/kubernetes"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// ApplyYAMLManifest applies YAML manifest(s) to the test cluster
// The yamlContent can contain multiple YAML documents separated by "---"
// namespace parameter is optional - if empty, uses namespace from manifest or "default"
func ApplyYAMLManifest(ctx context.Context, kubeconfig *rest.Config, yamlContent string, namespace string) error {
	applyClient, err := k8sapply.NewApplyClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create apply client: %w", err)
	}

	logger.Debug("Applying YAML manifest to cluster")
	if err := applyClient.ApplyYAML(ctx, yamlContent, namespace); err != nil {
		return fmt.Errorf("failed to apply YAML: %w", err)
	}

	logger.Success("YAML manifest applied successfully")
	return nil
}

// ApplyYAMLFile applies YAML manifest(s) from a file to the test cluster
func ApplyYAMLFile(ctx context.Context, kubeconfig *rest.Config, filePath string, namespace string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	logger.Debug("Applying YAML manifest from file: %s", filePath)
	return ApplyYAMLManifest(ctx, kubeconfig, string(content), namespace)
}

// CreateYAMLManifest creates resources from YAML manifest(s) in the test cluster
// Unlike ApplyYAMLManifest, this will fail if resources already exist
func CreateYAMLManifest(ctx context.Context, kubeconfig *rest.Config, yamlContent string, namespace string) error {
	applyClient, err := k8sapply.NewApplyClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create apply client: %w", err)
	}

	logger.Debug("Creating resources from YAML manifest")
	if err := applyClient.CreateYAML(ctx, yamlContent, namespace); err != nil {
		return fmt.Errorf("failed to create resources: %w", err)
	}

	logger.Success("Resources created successfully")
	return nil
}

// CreateYAMLFile creates resources from a YAML file in the test cluster
func CreateYAMLFile(ctx context.Context, kubeconfig *rest.Config, filePath string, namespace string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	logger.Debug("Creating resources from file: %s", filePath)
	return CreateYAMLManifest(ctx, kubeconfig, string(content), namespace)
}

// WaitForStorageClass waits for a storage class to become available
func WaitForStorageClass(ctx context.Context, kubeconfig *rest.Config, storageClassName string, timeout time.Duration) error {
	logger.Debug("Waiting for StorageClass %s to become available (timeout: %v)", storageClassName, timeout)

	// Create clientset from kubeconfig
	clientset, err := k8sclient.NewForConfig(kubeconfig)
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
		time.Sleep(2 * time.Second)
	}
}
