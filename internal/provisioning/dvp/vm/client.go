/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package vm

import (
	"context"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

type Client interface {
	GetClusterVirtualImage(ctx context.Context, name string) (*v1alpha2.ClusterVirtualImage, error)
	CreateClusterVirtualImage(ctx context.Context, cvi *v1alpha2.ClusterVirtualImage) error

	GetVirtualDisk(ctx context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error)
	CreateVirtualDisk(ctx context.Context, vd *v1alpha2.VirtualDisk) error
	DeleteVirtualDisk(ctx context.Context, namespace, name string) error
	ListVirtualDisks(ctx context.Context, namespace string) ([]v1alpha2.VirtualDisk, error)

	GetVirtualMachine(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error)
	CreateVirtualMachine(ctx context.Context, machine *v1alpha2.VirtualMachine) error
	DeleteVirtualMachine(ctx context.Context, namespace, name string) error
	ListVirtualMachines(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachine, error)

	GetVirtualMachineClass(ctx context.Context, name string) (*v1alpha3.VirtualMachineClass, error)
	CreateVirtualMachineClass(ctx context.Context, class *v1alpha3.VirtualMachineClass) error
}

type clientAdapter struct {
	c *virtualization.Client
}

func NewClient(c *virtualization.Client) Client {
	return &clientAdapter{c: c}
}

func (a *clientAdapter) GetClusterVirtualImage(ctx context.Context, name string) (*v1alpha2.ClusterVirtualImage, error) {
	return a.c.ClusterVirtualImages().Get(ctx, name)
}

func (a *clientAdapter) CreateClusterVirtualImage(ctx context.Context, cvi *v1alpha2.ClusterVirtualImage) error {
	return a.c.ClusterVirtualImages().Create(ctx, cvi)
}

func (a *clientAdapter) GetVirtualDisk(ctx context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error) {
	return a.c.VirtualDisks().Get(ctx, namespace, name)
}

func (a *clientAdapter) CreateVirtualDisk(ctx context.Context, vd *v1alpha2.VirtualDisk) error {
	return a.c.VirtualDisks().Create(ctx, vd)
}

func (a *clientAdapter) DeleteVirtualDisk(ctx context.Context, namespace, name string) error {
	return a.c.VirtualDisks().Delete(ctx, namespace, name)
}

func (a *clientAdapter) ListVirtualDisks(ctx context.Context, namespace string) ([]v1alpha2.VirtualDisk, error) {
	return a.c.VirtualDisks().List(ctx, namespace)
}

func (a *clientAdapter) GetVirtualMachine(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error) {
	return a.c.VirtualMachines().Get(ctx, namespace, name)
}

func (a *clientAdapter) CreateVirtualMachine(ctx context.Context, machine *v1alpha2.VirtualMachine) error {
	return a.c.VirtualMachines().Create(ctx, machine)
}

func (a *clientAdapter) DeleteVirtualMachine(ctx context.Context, namespace, name string) error {
	return a.c.VirtualMachines().Delete(ctx, namespace, name)
}

func (a *clientAdapter) ListVirtualMachines(ctx context.Context, namespace string) ([]v1alpha2.VirtualMachine, error) {
	return a.c.VirtualMachines().List(ctx, namespace)
}

func (a *clientAdapter) GetVirtualMachineClass(ctx context.Context, name string) (*v1alpha3.VirtualMachineClass, error) {
	return a.c.VirtualMachineClasses().Get(ctx, name)
}

func (a *clientAdapter) CreateVirtualMachineClass(ctx context.Context, class *v1alpha3.VirtualMachineClass) error {
	return a.c.VirtualMachineClasses().Create(ctx, class)
}
