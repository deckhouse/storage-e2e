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

// VirtualDiskClient provides operations on VirtualDisk resources
type VirtualDiskClient struct {
	client client.Client
}

// Get retrieves a VirtualDisk by namespace and name
func (c *VirtualDiskClient) Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error) {
	vd := &v1alpha2.VirtualDisk{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.client.Get(ctx, key, vd); err != nil {
		return nil, fmt.Errorf("failed to get VirtualDisk %s/%s: %w", namespace, name, err)
	}
	return vd, nil
}

// List lists VirtualDisks in a namespace
func (c *VirtualDiskClient) List(ctx context.Context, namespace string) ([]v1alpha2.VirtualDisk, error) {
	list := &v1alpha2.VirtualDiskList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.client.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("failed to list VirtualDisks: %w", err)
	}
	return list.Items, nil
}

// Create creates a new VirtualDisk
func (c *VirtualDiskClient) Create(ctx context.Context, vd *v1alpha2.VirtualDisk) error {
	if err := c.client.Create(ctx, vd); err != nil {
		return fmt.Errorf("failed to create VirtualDisk %s/%s: %w", vd.Namespace, vd.Name, err)
	}
	return nil
}

// Update updates an existing VirtualDisk
func (c *VirtualDiskClient) Update(ctx context.Context, vd *v1alpha2.VirtualDisk) error {
	if err := c.client.Update(ctx, vd); err != nil {
		return fmt.Errorf("failed to update VirtualDisk %s/%s: %w", vd.Namespace, vd.Name, err)
	}
	return nil
}

// Delete deletes a VirtualDisk by namespace and name
func (c *VirtualDiskClient) Delete(ctx context.Context, namespace, name string) error {
	vd := &v1alpha2.VirtualDisk{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	if err := c.client.Delete(ctx, vd); err != nil {
		return fmt.Errorf("failed to delete VirtualDisk %s/%s: %w", namespace, name, err)
	}
	return nil
}
