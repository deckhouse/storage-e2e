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

package dvp

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
)

// virtClient is the narrow virtualization surface the DVP connect path needs:
// the vmIPResolver reads VirtualMachines and dvpDiskManager manages
// VirtualDisks and their attachments. It is a flat interface (not
// accessor-based) because Go method sets are invariant — the concrete
// *virtualization.Client returns concrete sub-clients that would not satisfy an
// accessor-returning interface — so it is bridged by virtClientAdapter and
// faked directly in tests. This mirrors the vm.Client seam.
type virtClient interface {
	GetVirtualMachine(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error)

	GetVirtualDisk(ctx context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error)
	CreateVirtualDisk(ctx context.Context, vd *v1alpha2.VirtualDisk) error
	DeleteVirtualDisk(ctx context.Context, namespace, name string) error

	GetVMBDA(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error)
	CreateVMBDA(ctx context.Context, vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) error
	DeleteVMBDA(ctx context.Context, namespace, name string) error
}

// virtFactory builds a virtClient for a base-cluster rest.Config. Routing it
// through deps lets ConnectTestCluster stay free of virtualization.NewClient so
// tests can inject a fake.
type virtFactory interface {
	New(ctx context.Context, kube *rest.Config) (virtClient, error)
}

// virtClientAdapter bridges the concrete *virtualization.Client to virtClient.
type virtClientAdapter struct {
	c *virtualization.Client
}

func newVirtClient(c *virtualization.Client) virtClient { return virtClientAdapter{c: c} }

func (a virtClientAdapter) GetVirtualMachine(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error) {
	return a.c.VirtualMachines().Get(ctx, namespace, name)
}

func (a virtClientAdapter) GetVirtualDisk(ctx context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error) {
	return a.c.VirtualDisks().Get(ctx, namespace, name)
}

func (a virtClientAdapter) CreateVirtualDisk(ctx context.Context, vd *v1alpha2.VirtualDisk) error {
	return a.c.VirtualDisks().Create(ctx, vd)
}

func (a virtClientAdapter) DeleteVirtualDisk(ctx context.Context, namespace, name string) error {
	return a.c.VirtualDisks().Delete(ctx, namespace, name)
}

func (a virtClientAdapter) GetVMBDA(ctx context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
	return a.c.VirtualMachineBlockDeviceAttachments().Get(ctx, namespace, name)
}

func (a virtClientAdapter) CreateVMBDA(ctx context.Context, vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) error {
	return a.c.VirtualMachineBlockDeviceAttachments().Create(ctx, vmbda)
}

func (a virtClientAdapter) DeleteVMBDA(ctx context.Context, namespace, name string) error {
	return a.c.VirtualMachineBlockDeviceAttachments().Delete(ctx, namespace, name)
}

// defaultVirtFactory builds a real virtClient over the base cluster.
type defaultVirtFactory struct{}

func (defaultVirtFactory) New(ctx context.Context, kube *rest.Config) (virtClient, error) {
	c, err := virtualization.NewClient(ctx, kube)
	if err != nil {
		return nil, fmt.Errorf("create virtualization client: %w", err)
	}
	return newVirtClient(c), nil
}

// Compile-time guarantees for the seam.
var (
	_ virtClient  = virtClientAdapter{}
	_ virtFactory = defaultVirtFactory{}
)
