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

// VMBDClient provides operations on VirtualMachineBlockDeviceAttachment resources
type VMBDClient interface {
	Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error)
	List(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachineBlockDeviceAttachment, error)
	Create(ctx context.Context, vmbd *v1alpha2.VirtualMachineBlockDeviceAttachment) error
	Update(ctx context.Context, vmbd *v1alpha2.VirtualMachineBlockDeviceAttachment) error
	Delete(ctx context.Context, namespace, name string) error
}

type vmbdClient struct {
	client client.Client
}

func (c *vmbdClient) Get(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
	vmbd := &v1alpha2.VirtualMachineBlockDeviceAttachment{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := c.client.Get(ctx, key, vmbd); err != nil {
		return nil, fmt.Errorf("failed to get VirtualMachineBlockDeviceAttachment %s/%s: %w", namespace, name, err)
	}
	return vmbd, nil
}

func (c *vmbdClient) List(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
	list := &v1alpha2.VirtualMachineBlockDeviceAttachmentList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.client.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("failed to list VirtualMachineBlockDeviceAttachments: %w", err)
	}
	return list.Items, nil
}

func (c *vmbdClient) Create(ctx context.Context, vmbd *v1alpha2.VirtualMachineBlockDeviceAttachment) error {
	if err := c.client.Create(ctx, vmbd); err != nil {
		return fmt.Errorf("failed to create VirtualMachineBlockDeviceAttachment %s/%s: %w", vmbd.Namespace, vmbd.Name, err)
	}
	return nil
}

func (c *vmbdClient) Update(ctx context.Context, vmbd *v1alpha2.VirtualMachineBlockDeviceAttachment) error {
	if err := c.client.Update(ctx, vmbd); err != nil {
		return fmt.Errorf("failed to update VirtualMachineBlockDeviceAttachment %s/%s: %w", vmbd.Namespace, vmbd.Name, err)
	}
	return nil
}

func (c *vmbdClient) Delete(ctx context.Context, namespace, name string) error {
	vmbd := &v1alpha2.VirtualMachineBlockDeviceAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	if err := c.client.Delete(ctx, vmbd); err != nil {
		return fmt.Errorf("failed to delete VirtualMachineBlockDeviceAttachment %s/%s: %w", namespace, name, err)
	}
	return nil
}
