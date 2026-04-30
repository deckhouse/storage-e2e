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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

// VirtualMachineClassClient provides operations on VirtualMachineClass resources (cluster-scoped).
// Uses API version v1alpha3 (storage/preferred); v1alpha2.VirtualMachineClass is deprecated upstream.
type VirtualMachineClassClient struct {
	client client.Client
}

// Get retrieves a VirtualMachineClass by name.
func (c *VirtualMachineClassClient) Get(ctx context.Context, name string) (*v1alpha3.VirtualMachineClass, error) {
	vmc := &v1alpha3.VirtualMachineClass{}
	key := client.ObjectKey{Name: name}
	if err := c.client.Get(ctx, key, vmc); err != nil {
		return nil, fmt.Errorf("failed to get VirtualMachineClass %s: %w", name, err)
	}
	return vmc, nil
}

// List lists all VirtualMachineClasses.
func (c *VirtualMachineClassClient) List(ctx context.Context) ([]v1alpha3.VirtualMachineClass, error) {
	list := &v1alpha3.VirtualMachineClassList{}
	if err := c.client.List(ctx, list); err != nil {
		return nil, fmt.Errorf("failed to list VirtualMachineClasses: %w", err)
	}
	return list.Items, nil
}

// Create creates a new VirtualMachineClass.
func (c *VirtualMachineClassClient) Create(ctx context.Context, vmc *v1alpha3.VirtualMachineClass) error {
	if err := c.client.Create(ctx, vmc); err != nil {
		return fmt.Errorf("failed to create VirtualMachineClass %s: %w", vmc.Name, err)
	}
	return nil
}

// Delete deletes a VirtualMachineClass by name.
func (c *VirtualMachineClassClient) Delete(ctx context.Context, name string) error {
	vmc := &v1alpha3.VirtualMachineClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := c.client.Delete(ctx, vmc); err != nil {
		return fmt.Errorf("failed to delete VirtualMachineClass %s: %w", name, err)
	}
	return nil
}
