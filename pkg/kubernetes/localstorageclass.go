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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

var LocalStorageClassGVR = schema.GroupVersionResource{
	Group:    "storage.deckhouse.io",
	Version:  "v1alpha1",
	Resource: "localstorageclasses",
}

type LocalStorageClassConfig struct {
	Name              string
	LVMVolumeGroups   []string // LVMVolumeGroup resource names
	LVMType           string   // "Thick" or "Thin"
	ThinPoolName      string   // required when LVMType is "Thin"
	ReclaimPolicy     string   // "Delete" or "Retain" (default: "Delete")
	VolumeBindingMode string   // "WaitForFirstConsumer" or "Immediate" (default: "WaitForFirstConsumer")
}

func CreateLocalStorageClass(ctx context.Context, kubeconfig *rest.Config, cfg LocalStorageClassConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("LocalStorageClass name is required")
	}
	if len(cfg.LVMVolumeGroups) == 0 {
		return fmt.Errorf("at least one LVMVolumeGroup is required")
	}
	if cfg.LVMType == "" {
		cfg.LVMType = "Thick"
	}
	if cfg.LVMType == "Thin" && cfg.ThinPoolName == "" {
		return fmt.Errorf("ThinPoolName is required for Thin LVM type")
	}
	if cfg.ReclaimPolicy == "" {
		cfg.ReclaimPolicy = "Delete"
	}
	if cfg.VolumeBindingMode == "" {
		cfg.VolumeBindingMode = "WaitForFirstConsumer"
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	lvgRefs := make([]interface{}, len(cfg.LVMVolumeGroups))
	for i, name := range cfg.LVMVolumeGroups {
		ref := map[string]interface{}{
			"name": name,
		}
		if cfg.LVMType == "Thin" {
			ref["thin"] = map[string]interface{}{
				"poolName": cfg.ThinPoolName,
			}
		}
		lvgRefs[i] = ref
	}

	lsc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "storage.deckhouse.io/v1alpha1",
			"kind":       "LocalStorageClass",
			"metadata": map[string]interface{}{
				"name": cfg.Name,
			},
			"spec": map[string]interface{}{
				"lvm": map[string]interface{}{
					"lvmVolumeGroups": lvgRefs,
					"type":            cfg.LVMType,
				},
				"reclaimPolicy":     cfg.ReclaimPolicy,
				"volumeBindingMode": cfg.VolumeBindingMode,
			},
		},
	}

	logger.Info("Creating LocalStorageClass %s (type=%s, lvmVolumeGroups=%v)", cfg.Name, cfg.LVMType, cfg.LVMVolumeGroups)
	_, err = dynamicClient.Resource(LocalStorageClassGVR).Create(ctx, lsc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("LocalStorageClass %s already exists, skipping create", cfg.Name)
			return nil
		}
		return fmt.Errorf("failed to create LocalStorageClass %s: %w", cfg.Name, err)
	}
	logger.Success("LocalStorageClass %s created", cfg.Name)
	return nil
}

// WaitForLocalStorageClassCreated waits for the LocalStorageClass CR status to indicate
// that the controller has created the corresponding StorageClass.
func WaitForLocalStorageClassCreated(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	logger.Debug("Waiting for LocalStorageClass %s to be Created (timeout: %v)", name, timeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for LocalStorageClass %s to be Created", name)
		}

		obj, err := dynamicClient.Resource(LocalStorageClassGVR).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			if phase == "Created" {
				logger.Success("LocalStorageClass %s is Created", name)
				return nil
			}
			logger.Debug("LocalStorageClass %s phase: %s, waiting...", name, phase)
		}

		time.Sleep(5 * time.Second)
	}
}
