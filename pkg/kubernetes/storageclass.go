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

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		_, err := clientset.StorageV1().StorageClasses().Get(ctx, storageClassName, metav1.GetOptions{})
		if err == nil {
			logger.Success("StorageClass %s is available", storageClassName)
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for StorageClass %s: %w", storageClassName, ctx.Err())
		case <-ticker.C:
		}
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

// GetStorageClass returns the StorageClass with the given name, or (nil, nil) if it does not exist.
func GetStorageClass(ctx context.Context, kubeconfig *rest.Config, name string) (*storagev1.StorageClass, error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	sc, err := clientset.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get StorageClass %s: %w", name, err)
	}
	return sc, nil
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

// StorageClassCreateConfig describes a StorageClass to create via CreateStorageClass.
type StorageClassCreateConfig struct {
	Name              string
	Provisioner       string
	Parameters        map[string]string
	VolumeBindingMode storagev1.VolumeBindingMode
	ReclaimPolicy     corev1.PersistentVolumeReclaimPolicy
	AllowExpansion    bool
	MakeDefault       bool
	AdditionalLabels  map[string]string
	AdditionalAnnot   map[string]string
}

// CreateStorageClass creates a StorageClass from cfg, or no-ops if it already exists.
func CreateStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg StorageClassCreateConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("storage class name is required")
	}
	if cfg.Provisioner == "" {
		return fmt.Errorf("provisioner is required")
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	annotations := map[string]string{}
	for k, v := range cfg.AdditionalAnnot {
		annotations[k] = v
	}
	if cfg.MakeDefault {
		annotations["storageclass.kubernetes.io/is-default-class"] = "true"
		annotations["storageclass.beta.kubernetes.io/is-default-class"] = "true"
	}

	labels := map[string]string{}
	for k, v := range cfg.AdditionalLabels {
		labels[k] = v
	}

	sc := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        cfg.Name,
			Labels:      labels,
			Annotations: annotations,
		},
		Provisioner:          cfg.Provisioner,
		Parameters:           cfg.Parameters,
		ReclaimPolicy:        &cfg.ReclaimPolicy,
		AllowVolumeExpansion: &cfg.AllowExpansion,
		VolumeBindingMode:    &cfg.VolumeBindingMode,
	}

	logger.Info("Creating StorageClass %s (provisioner=%s)", cfg.Name, cfg.Provisioner)
	_, err = clientset.StorageV1().StorageClasses().Create(ctx, sc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("StorageClass %s already exists, skipping create", cfg.Name)
			return nil
		}
		return fmt.Errorf("failed to create StorageClass %s: %w", cfg.Name, err)
	}
	logger.Success("StorageClass %s created", cfg.Name)
	return nil
}
