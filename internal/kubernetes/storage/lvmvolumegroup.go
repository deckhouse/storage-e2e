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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

// LVMVolumeGroupClient provides operations on LVMVolumeGroup resources
type LVMVolumeGroupClient struct {
	client client.Client
}

// NewLVMVolumeGroupClient creates a new LVMVolumeGroup client from a rest.Config
// It uses controller-runtime client which provides type-safe access to CRDs.
// Includes retry logic for transient network errors during client creation,
// since controller-runtime client.New() performs API discovery which can fail
// with TLS handshake timeouts or other transient network issues.
func NewLVMVolumeGroupClient(ctx context.Context, config *rest.Config) (*LVMVolumeGroupClient, error) {
	return retry.Do(ctx, retry.DefaultConfig, "create LVMVolumeGroup client", func() (*LVMVolumeGroupClient, error) {
		scheme := runtime.NewScheme()

		// Register sds-node-configurator API types with the scheme
		if err := snc.AddToScheme(scheme); err != nil {
			return nil, fmt.Errorf("failed to add sds-node-configurator scheme: %w", err)
		}

		cl, err := client.New(config, client.Options{Scheme: scheme})
		if err != nil {
			return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
		}

		return &LVMVolumeGroupClient{client: cl}, nil
	})
}

// List lists all LVMVolumeGroups in the cluster
func (c *LVMVolumeGroupClient) List(ctx context.Context) (*snc.LVMVolumeGroupList, error) {
	var lvgList snc.LVMVolumeGroupList
	if err := c.client.List(ctx, &lvgList); err != nil {
		return nil, fmt.Errorf("failed to list LVMVolumeGroups: %w", err)
	}
	return &lvgList, nil
}

// Get retrieves an LVMVolumeGroup by name
func (c *LVMVolumeGroupClient) Get(ctx context.Context, name string) (*snc.LVMVolumeGroup, error) {
	var lvg snc.LVMVolumeGroup
	if err := c.client.Get(ctx, client.ObjectKey{Name: name}, &lvg); err != nil {
		return nil, fmt.Errorf("failed to get LVMVolumeGroup %s: %w", name, err)
	}
	return &lvg, nil
}

// Create creates a new LVMVolumeGroup for a specific node
func (c *LVMVolumeGroupClient) Create(ctx context.Context, name, nodeName string, blockDeviceNames []string, actualVGName string) error {
	lvg := &snc.LVMVolumeGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: snc.SchemeGroupVersion.String(),
			Kind:       "LVMVolumeGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMVolumeGroupSpec{
			Type: "Local",
			Local: snc.LVMVolumeGroupLocalSpec{
				NodeName: nodeName,
			},
			BlockDeviceSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "kubernetes.io/metadata.name",
						Operator: metav1.LabelSelectorOpIn,
						Values:   blockDeviceNames,
					},
				},
			},
			ActualVGNameOnTheNode: actualVGName,
		},
	}

	if err := c.client.Create(ctx, lvg); err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup %s: %w", name, err)
	}

	return nil
}

// CreateWithThinPools creates a new LVMVolumeGroup with thin pools
func (c *LVMVolumeGroupClient) CreateWithThinPools(ctx context.Context, name, nodeName string, blockDeviceNames []string, actualVGName string, thinPools []snc.LVMVolumeGroupThinPoolSpec) error {
	lvg := &snc.LVMVolumeGroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: snc.SchemeGroupVersion.String(),
			Kind:       "LVMVolumeGroup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMVolumeGroupSpec{
			Type: "Local",
			Local: snc.LVMVolumeGroupLocalSpec{
				NodeName: nodeName,
			},
			BlockDeviceSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "kubernetes.io/metadata.name",
						Operator: metav1.LabelSelectorOpIn,
						Values:   blockDeviceNames,
					},
				},
			},
			ActualVGNameOnTheNode: actualVGName,
			ThinPools:             thinPools,
		},
	}

	if err := c.client.Create(ctx, lvg); err != nil {
		return fmt.Errorf("failed to create LVMVolumeGroup %s: %w", name, err)
	}

	return nil
}

// Delete deletes an LVMVolumeGroup by name
func (c *LVMVolumeGroupClient) Delete(ctx context.Context, name string) error {
	lvg := &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if err := c.client.Delete(ctx, lvg); err != nil {
		return fmt.Errorf("failed to delete LVMVolumeGroup %s: %w", name, err)
	}

	return nil
}

// WaitForReady waits for an LVMVolumeGroup to become Ready
func (c *LVMVolumeGroupClient) WaitForReady(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for LVMVolumeGroup %s to become Ready", name)
		}

		lvg, err := c.Get(ctx, name)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		if lvg.Status.Phase == "Ready" {
			return nil
		}

		time.Sleep(2 * time.Second)
	}
}

// WaitForDeletion waits for an LVMVolumeGroup to be deleted
func (c *LVMVolumeGroupClient) WaitForDeletion(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for LVMVolumeGroup %s to be deleted", name)
		}

		_, err := c.Get(ctx, name)
		if err != nil {
			// Assume deleted if we can't get it
			return nil
		}

		time.Sleep(2 * time.Second)
	}
}

// IsReady checks if an LVMVolumeGroup is in Ready phase
func (c *LVMVolumeGroupClient) IsReady(ctx context.Context, name string) (bool, error) {
	lvg, err := c.Get(ctx, name)
	if err != nil {
		return false, err
	}

	return lvg.Status.Phase == "Ready", nil
}
