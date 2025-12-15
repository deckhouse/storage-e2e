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

// ClusterVirtualImageClient provides operations on ClusterVirtualImage resources
// Note: ClusterVirtualImage is a cluster-scoped resource (no namespace)
type ClusterVirtualImageClient interface {
	Get(ctx context.Context, name string) (*v1alpha2.ClusterVirtualImage, error)
	List(ctx context.Context) ([]v1alpha2.ClusterVirtualImage, error)
	Create(ctx context.Context, cvmi *v1alpha2.ClusterVirtualImage) error
	Update(ctx context.Context, cvmi *v1alpha2.ClusterVirtualImage) error
	Delete(ctx context.Context, name string) error
}

type clusterVirtualImageClient struct {
	client client.Client
}

func (c *clusterVirtualImageClient) Get(ctx context.Context, name string) (*v1alpha2.ClusterVirtualImage, error) {
	cvmi := &v1alpha2.ClusterVirtualImage{}
	key := client.ObjectKey{Name: name}
	if err := c.client.Get(ctx, key, cvmi); err != nil {
		return nil, fmt.Errorf("failed to get ClusterVirtualImage %s: %w", name, err)
	}
	return cvmi, nil
}

func (c *clusterVirtualImageClient) List(ctx context.Context) ([]v1alpha2.ClusterVirtualImage, error) {
	list := &v1alpha2.ClusterVirtualImageList{}
	if err := c.client.List(ctx, list); err != nil {
		return nil, fmt.Errorf("failed to list ClusterVirtualImages: %w", err)
	}
	return list.Items, nil
}

func (c *clusterVirtualImageClient) Create(ctx context.Context, cvmi *v1alpha2.ClusterVirtualImage) error {
	if err := c.client.Create(ctx, cvmi); err != nil {
		return fmt.Errorf("failed to create ClusterVirtualImage %s: %w", cvmi.Name, err)
	}
	return nil
}

func (c *clusterVirtualImageClient) Update(ctx context.Context, cvmi *v1alpha2.ClusterVirtualImage) error {
	if err := c.client.Update(ctx, cvmi); err != nil {
		return fmt.Errorf("failed to update ClusterVirtualImage %s: %w", cvmi.Name, err)
	}
	return nil
}

func (c *clusterVirtualImageClient) Delete(ctx context.Context, name string) error {
	cvmi := &v1alpha2.ClusterVirtualImage{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := c.client.Delete(ctx, cvmi); err != nil {
		return fmt.Errorf("failed to delete ClusterVirtualImage %s: %w", name, err)
	}
	return nil
}
