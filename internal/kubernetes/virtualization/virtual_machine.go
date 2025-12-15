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

// VirtualMachineClient provides operations on VirtualMachine resources
type VirtualMachineClient interface {
	Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error)
	List(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachine, error)
	Create(ctx context.Context, vm *v1alpha2.VirtualMachine) error
	Update(ctx context.Context, vm *v1alpha2.VirtualMachine) error
	Delete(ctx context.Context, namespace, name string) error
}

type virtualMachineClient struct {
	client client.Client
}

func (c *virtualMachineClient) Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error) {
	vm := &v1alpha2.VirtualMachine{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.client.Get(ctx, key, vm); err != nil {
		return nil, fmt.Errorf("failed to get VirtualMachine %s/%s: %w", namespace, name, err)
	}
	return vm, nil
}

func (c *virtualMachineClient) List(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachine, error) {
	list := &v1alpha2.VirtualMachineList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.client.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("failed to list VirtualMachines: %w", err)
	}
	return list.Items, nil
}

func (c *virtualMachineClient) Create(ctx context.Context, vm *v1alpha2.VirtualMachine) error {
	if err := c.client.Create(ctx, vm); err != nil {
		return fmt.Errorf("failed to create VirtualMachine %s/%s: %w", vm.Namespace, vm.Name, err)
	}
	return nil
}

func (c *virtualMachineClient) Update(ctx context.Context, vm *v1alpha2.VirtualMachine) error {
	if err := c.client.Update(ctx, vm); err != nil {
		return fmt.Errorf("failed to update VirtualMachine %s/%s: %w", vm.Namespace, vm.Name, err)
	}
	return nil
}

func (c *virtualMachineClient) Delete(ctx context.Context, namespace, name string) error {
	vm := &v1alpha2.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	if err := c.client.Delete(ctx, vm); err != nil {
		return fmt.Errorf("failed to delete VirtualMachine %s/%s: %w", namespace, name, err)
	}
	return nil
}
