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

// fakeVirt is an in-memory virtClient for hermetic dvp connect-path tests.
type fakeVirt struct {
	mu sync.Mutex

	vms    map[string]*v1alpha2.VirtualMachine
	disks  map[string]*v1alpha2.VirtualDisk
	vmbdas map[string]*v1alpha2.VirtualMachineBlockDeviceAttachment

	// Injected errors (nil = success).
	getVMErr       error
	createDiskErr  error
	createVMBDAErr error

	// vmbdaPhase, when set, overrides the phase returned by every VMBDA Get.
	vmbdaPhase v1alpha2.BlockDeviceAttachmentPhase

	// Ordered mutation log: "create-vd:<name>", "create-vmbda:<name>",
	// "delete-vd:<name>", "delete-vmbda:<name>".
	ops []string
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

// seedDisk inserts a VirtualDisk verbatim (labels/spec preserved).
func (f *fakeVirt) seedDisk(vd *v1alpha2.VirtualDisk) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disks[fvKey(vd.Namespace, vd.Name)] = vd.DeepCopy()
}

func (f *fakeVirt) recordedOps() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
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

func (f *fakeVirt) CreateVirtualDisk(_ context.Context, vd *v1alpha2.VirtualDisk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createDiskErr != nil {
		return f.createDiskErr
	}
	f.disks[fvKey(vd.Namespace, vd.Name)] = vd.DeepCopy()
	f.ops = append(f.ops, "create-vd:"+vd.Name)
	return nil
}

func (f *fakeVirt) DeleteVirtualDisk(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fvKey(namespace, name)
	if _, ok := f.disks[key]; !ok {
		return fvNotFound("virtualdisks", name)
	}
	delete(f.disks, key)
	f.ops = append(f.ops, "delete-vd:"+name)
	return nil
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

func (f *fakeVirt) ListVirtualDisks(_ context.Context, namespace string) ([]v1alpha2.VirtualDisk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]v1alpha2.VirtualDisk, 0, len(f.disks))
	for _, vd := range f.disks {
		if namespace != "" && vd.Namespace != namespace {
			continue
		}
		out = append(out, *vd.DeepCopy())
	}
	return out, nil
}

func (f *fakeVirt) CreateVirtualMachineBlockDeviceAttachment(_ context.Context, a *v1alpha2.VirtualMachineBlockDeviceAttachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createVMBDAErr != nil {
		return f.createVMBDAErr
	}
	f.vmbdas[fvKey(a.Namespace, a.Name)] = a.DeepCopy()
	f.ops = append(f.ops, "create-vmbda:"+a.Name)
	return nil
}

func (f *fakeVirt) DeleteVirtualMachineBlockDeviceAttachment(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fvKey(namespace, name)
	if _, ok := f.vmbdas[key]; !ok {
		return fvNotFound("virtualmachineblockdeviceattachments", name)
	}
	delete(f.vmbdas, key)
	f.ops = append(f.ops, "delete-vmbda:"+name)
	return nil
}

func (f *fakeVirt) GetVirtualMachineBlockDeviceAttachment(_ context.Context, namespace, name string) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.vmbdas[fvKey(namespace, name)]
	if !ok {
		return nil, fvNotFound("virtualmachineblockdeviceattachments", name)
	}
	out := a.DeepCopy()
	if f.vmbdaPhase != "" {
		out.Status.Phase = f.vmbdaPhase
	}
	return out, nil
}

// Compile-time guarantee the fake satisfies the seam.
var _ virtClient = (*fakeVirt)(nil)
