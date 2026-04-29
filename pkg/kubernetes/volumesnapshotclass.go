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

var VolumeSnapshotClassGVR = schema.GroupVersionResource{
	Group:    "snapshot.storage.k8s.io",
	Version:  "v1",
	Resource: "volumesnapshotclasses",
}

type VolumeSnapshotClassConfig struct {
	Name           string
	Driver         string
	DeletionPolicy string // "Delete" or "Retain"
	Parameters     map[string]string
	MakeDefault    bool
}

func CreateVolumeSnapshotClass(ctx context.Context, kubeconfig *rest.Config, cfg VolumeSnapshotClassConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("volume snapshot class name is required")
	}
	if cfg.Driver == "" {
		return fmt.Errorf("driver is required")
	}
	if cfg.DeletionPolicy == "" {
		cfg.DeletionPolicy = "Delete"
	}

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	annotations := map[string]interface{}{}
	if cfg.MakeDefault {
		annotations["snapshot.storage.kubernetes.io/is-default-class"] = "true"
	}

	parameters := map[string]interface{}{}
	for k, v := range cfg.Parameters {
		parameters[k] = v
	}

	vsc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "snapshot.storage.k8s.io/v1",
			"kind":       "VolumeSnapshotClass",
			"metadata": map[string]interface{}{
				"name":        cfg.Name,
				"annotations": annotations,
			},
			"driver":         cfg.Driver,
			"deletionPolicy": cfg.DeletionPolicy,
			"parameters":     parameters,
		},
	}

	logger.Info("Creating VolumeSnapshotClass %s (driver=%s, deletionPolicy=%s)", cfg.Name, cfg.Driver, cfg.DeletionPolicy)
	_, err = dynamicClient.Resource(VolumeSnapshotClassGVR).Create(ctx, vsc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("VolumeSnapshotClass %s already exists, skipping create", cfg.Name)
			return nil
		}
		return fmt.Errorf("failed to create VolumeSnapshotClass %s: %w", cfg.Name, err)
	}
	logger.Success("VolumeSnapshotClass %s created", cfg.Name)
	return nil
}

func WaitForVolumeSnapshotClass(ctx context.Context, kubeconfig *rest.Config, name string, timeout time.Duration) error {
	logger.Debug("Waiting for VolumeSnapshotClass %s to become available (timeout: %v)", name, timeout)

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
			return fmt.Errorf("timeout waiting for VolumeSnapshotClass %s", name)
		}

		_, err := dynamicClient.Resource(VolumeSnapshotClassGVR).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			logger.Success("VolumeSnapshotClass %s is available", name)
			return nil
		}

		time.Sleep(5 * time.Second)
	}
}
