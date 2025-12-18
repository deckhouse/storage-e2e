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

package virtualization

import (
	"context"
	"fmt"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VirtualImageClient provides operations on VirtualImage resources
// Note: VirtualImage is a namespace-scoped resource
type VirtualImageClient interface {
	Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualImage, error)
	List(ctx context.Context, namespace string) ([]v1alpha2.VirtualImage, error)
	Create(ctx context.Context, vi *v1alpha2.VirtualImage) error
	Update(ctx context.Context, vi *v1alpha2.VirtualImage) error
	Delete(ctx context.Context, namespace, name string) error
}

type virtualImageClient struct {
	client client.Client
}

func (c *virtualImageClient) Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualImage, error) {
	vi := &v1alpha2.VirtualImage{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.client.Get(ctx, key, vi); err != nil {
		return nil, fmt.Errorf("failed to get VirtualImage %s/%s: %w", namespace, name, err)
	}
	return vi, nil
}

func (c *virtualImageClient) List(ctx context.Context, namespace string) ([]v1alpha2.VirtualImage, error) {
	list := &v1alpha2.VirtualImageList{}
	if err := c.client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list VirtualImages in namespace %s: %w", namespace, err)
	}
	return list.Items, nil
}

func (c *virtualImageClient) Create(ctx context.Context, vi *v1alpha2.VirtualImage) error {
	if err := c.client.Create(ctx, vi); err != nil {
		return fmt.Errorf("failed to create VirtualImage %s/%s: %w", vi.Namespace, vi.Name, err)
	}
	return nil
}

func (c *virtualImageClient) Update(ctx context.Context, vi *v1alpha2.VirtualImage) error {
	if err := c.client.Update(ctx, vi); err != nil {
		return fmt.Errorf("failed to update VirtualImage %s/%s: %w", vi.Namespace, vi.Name, err)
	}
	return nil
}

func (c *virtualImageClient) Delete(ctx context.Context, namespace, name string) error {
	vi := &v1alpha2.VirtualImage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := c.client.Delete(ctx, vi); err != nil {
		return fmt.Errorf("failed to delete VirtualImage %s/%s: %w", namespace, name, err)
	}
	return nil
}

