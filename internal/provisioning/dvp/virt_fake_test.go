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
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
)

// fakeVirt is an in-memory virtClient for hermetic dvp connect-path and
// disk-manager tests.
type fakeVirt struct {
	mu sync.Mutex

	vms    map[string]*v1alpha2.VirtualMachine
	disks  map[string]*v1alpha2.VirtualDisk
	vmbdas map[string]*v1alpha2.VirtualMachineBlockDeviceAttachment

	// Hooks run under the lock right after a successful create; tests use
	// them to set the status a controller would converge to.
	onCreateDisk  func(vd *v1alpha2.VirtualDisk)
	onUpdateDisk  func(vd *v1alpha2.VirtualDisk)
	onCreateVMBDA func(vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment)

	// Injected errors (nil = success).
	getVMErr       error
	createDiskErr  error
	updateDiskErr  error
	deleteDiskErr  error
	createVMBDAErr error
	deleteVMBDAErr error
}

func newFakeVirt() *fakeVirt {
	return &fakeVirt{
		vms:    map[string]*v1alpha2.VirtualMachine{},
		disks:  map[string]*v1alpha2.VirtualDisk{},
		vmbdas: map[string]*v1alpha2.VirtualMachineBlockDeviceAttachment{},
	}
}

func fvKey(namespace, name string) string { return namespace + "/" + name }

func fvNotFound(resource, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "virtualization.deckhouse.io", Resource: resource}, name)
}

// seedVM inserts a VirtualMachine with the given namespace/name/IP.
func (f *fakeVirt) seedVM(namespace, name, ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vm := &v1alpha2.VirtualMachine{}
	vm.Namespace = namespace
	vm.Name = name
	vm.Status.IPAddress = ip
	f.vms[fvKey(namespace, name)] = vm
}

func (f *fakeVirt) GetVirtualMachine(_ context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getVMErr != nil {
		return nil, f.getVMErr
	}
	vm, ok := f.vms[fvKey(namespace, name)]
	if !ok {
		return nil, fvNotFound("virtualmachines", name)
	}
	return vm.DeepCopy(), nil
}

func (f *fakeVirt) GetVirtualDisk(_ context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vd, ok := f.disks[fvKey(namespace, name)]
	if !ok {
		return nil, fvNotFound("virtualdisks", name)
	}
	return vd.DeepCopy(), nil
}

func (f *fakeVirt) CreateVirtualDisk(_ context.Context, vd *v1alpha2.VirtualDisk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createDiskErr != nil {
		return f.createDiskErr
	}
	key := fvKey(vd.Namespace, vd.Name)
	if _, ok := f.disks[key]; ok {
		return apierrors.NewAlreadyExists(schema.GroupResource{Group: "virtualization.deckhouse.io", Resource: "virtualdisks"}, vd.Name)
	}
	stored := vd.DeepCopy()
	if f.onCreateDisk != nil {
		f.onCreateDisk(stored)
	}
	f.disks[key] = stored
	return nil
}

func (f *fakeVirt) UpdateVirtualDisk(_ context.Context, vd *v1alpha2.VirtualDisk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateDiskErr != nil {
		return f.updateDiskErr
	}
	key := fvKey(vd.Namespace, vd.Name)
	if _, ok := f.disks[key]; !ok {
		return fvNotFound("virtualdisks", vd.Name)
	}
	stored := vd.DeepCopy()
	if f.onUpdateDisk != nil {
		f.onUpdateDisk(stored)
	}
	f.disks[key] = stored
	return nil
}

func (f *fakeVirt) DeleteVirtualDisk(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteDiskErr != nil {
		return f.deleteDiskErr
	}
	key := fvKey(namespace, name)
	if _, ok := f.disks[key]; !ok {
		return fvNotFound("virtualdisks", name)
	}
	delete(f.disks, key)
	return nil
}

func (f *fakeVirt) GetVMBDA(_ context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vmbda, ok := f.vmbdas[fvKey(namespace, name)]
	if !ok {
		return nil, fvNotFound("virtualmachineblockdeviceattachments", name)
	}
	return vmbda.DeepCopy(), nil
}

func (f *fakeVirt) CreateVMBDA(_ context.Context, vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createVMBDAErr != nil {
		return f.createVMBDAErr
	}
	key := fvKey(vmbda.Namespace, vmbda.Name)
	if _, ok := f.vmbdas[key]; ok {
		return apierrors.NewAlreadyExists(schema.GroupResource{Group: "virtualization.deckhouse.io", Resource: "virtualmachineblockdeviceattachments"}, vmbda.Name)
	}
	stored := vmbda.DeepCopy()
	if f.onCreateVMBDA != nil {
		f.onCreateVMBDA(stored)
	}
	f.vmbdas[key] = stored
	return nil
}

func (f *fakeVirt) DeleteVMBDA(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteVMBDAErr != nil {
		return f.deleteVMBDAErr
	}
	key := fvKey(namespace, name)
	if _, ok := f.vmbdas[key]; !ok {
		return fvNotFound("virtualmachineblockdeviceattachments", name)
	}
	delete(f.vmbdas, key)
	return nil
}

// Compile-time guarantee the fake satisfies the seam.
var _ virtClient = (*fakeVirt)(nil)
