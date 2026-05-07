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

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

type StorageClassCreateConfig struct {
	Name               string
	Provisioner        string
	Parameters         map[string]string
	VolumeBindingMode  storagev1.VolumeBindingMode
	ReclaimPolicy      corev1.PersistentVolumeReclaimPolicy
	AllowExpansion     bool
	MakeDefault        bool
	AdditionalLabels   map[string]string
	AdditionalAnnot    map[string]string
}

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

