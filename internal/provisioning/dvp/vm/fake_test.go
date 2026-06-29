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
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

type fakeClient struct {
	mu sync.Mutex

	cvis map[string]*v1alpha2.ClusterVirtualImage
	vds  map[string]*v1alpha2.VirtualDisk
	vms  map[string]*v1alpha2.VirtualMachine
	vmcs map[string]*v1alpha3.VirtualMachineClass

	createCalls map[string]int

	onGetCVI func(*v1alpha2.ClusterVirtualImage)
	onGetVM  func(*v1alpha2.VirtualMachine)
	onGetVMC func(*v1alpha3.VirtualMachineClass)
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		cvis:        map[string]*v1alpha2.ClusterVirtualImage{},
		vds:         map[string]*v1alpha2.VirtualDisk{},
		vms:         map[string]*v1alpha2.VirtualMachine{},
		vmcs:        map[string]*v1alpha3.VirtualMachineClass{},
		createCalls: map[string]int{},
	}
}

func nsKey(namespace, name string) string { return namespace + "/" + name }

func notFound(resource, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "virtualization.deckhouse.io", Resource: resource}, name)
}

func alreadyExists(resource, name string) error {
	return apierrors.NewAlreadyExists(schema.GroupResource{Group: "virtualization.deckhouse.io", Resource: resource}, name)
}

func (f *fakeClient) GetClusterVirtualImage(_ context.Context, name string) (*v1alpha2.ClusterVirtualImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cvi, ok := f.cvis[name]
	if !ok {
		return nil, notFound("clustervirtualimages", name)
	}
	out := cvi.DeepCopy()
	if f.onGetCVI != nil {
		f.onGetCVI(out)
	}
	return out, nil
}

func (f *fakeClient) CreateClusterVirtualImage(_ context.Context, cvi *v1alpha2.ClusterVirtualImage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls["cvi"]++
	if _, ok := f.cvis[cvi.Name]; ok {
		return alreadyExists("clustervirtualimages", cvi.Name)
	}
	f.cvis[cvi.Name] = cvi.DeepCopy()
	return nil
}

func (f *fakeClient) DeleteClusterVirtualImage(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.cvis[name]; !ok {
		return notFound("clustervirtualimages", name)
	}
	delete(f.cvis, name)
	return nil
}

func (f *fakeClient) ListClusterVirtualImages(_ context.Context) ([]v1alpha2.ClusterVirtualImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]v1alpha2.ClusterVirtualImage, 0, len(f.cvis))
	for _, cvi := range f.cvis {
		out = append(out, *cvi.DeepCopy())
	}
	return out, nil
}

func (f *fakeClient) GetVirtualDisk(_ context.Context, namespace, name string) (*v1alpha2.VirtualDisk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vd, ok := f.vds[nsKey(namespace, name)]
	if !ok {
		return nil, notFound("virtualdisks", name)
	}
	return vd.DeepCopy(), nil
}

func (f *fakeClient) CreateVirtualDisk(_ context.Context, vd *v1alpha2.VirtualDisk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls["vd"]++
	key := nsKey(vd.Namespace, vd.Name)
	if _, ok := f.vds[key]; ok {
		return alreadyExists("virtualdisks", vd.Name)
	}
	f.vds[key] = vd.DeepCopy()
	return nil
}

func (f *fakeClient) DeleteVirtualDisk(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := nsKey(namespace, name)
	if _, ok := f.vds[key]; !ok {
		return notFound("virtualdisks", name)
	}
	delete(f.vds, key)
	return nil
}

func (f *fakeClient) ListVirtualDisks(_ context.Context, namespace string) ([]v1alpha2.VirtualDisk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]v1alpha2.VirtualDisk, 0, len(f.vds))
	for _, vd := range f.vds {
		if namespace != "" && vd.Namespace != namespace {
			continue
		}
		out = append(out, *vd.DeepCopy())
	}
	return out, nil
}

func (f *fakeClient) GetVirtualMachine(_ context.Context, namespace, name string) (*v1alpha2.VirtualMachine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	machine, ok := f.vms[nsKey(namespace, name)]
	if !ok {
		return nil, notFound("virtualmachines", name)
	}
	out := machine.DeepCopy()
	if f.onGetVM != nil {
		f.onGetVM(out)
	}
	return out, nil
}

func (f *fakeClient) CreateVirtualMachine(_ context.Context, machine *v1alpha2.VirtualMachine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls["vm"]++
	key := nsKey(machine.Namespace, machine.Name)
	if _, ok := f.vms[key]; ok {
		return alreadyExists("virtualmachines", machine.Name)
	}
	f.vms[key] = machine.DeepCopy()
	return nil
}

func (f *fakeClient) DeleteVirtualMachine(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := nsKey(namespace, name)
	if _, ok := f.vms[key]; !ok {
		return notFound("virtualmachines", name)
	}
	delete(f.vms, key)
	return nil
}

func (f *fakeClient) ListVirtualMachines(_ context.Context, namespace string) ([]v1alpha2.VirtualMachine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]v1alpha2.VirtualMachine, 0, len(f.vms))
	for _, machine := range f.vms {
		if namespace != "" && machine.Namespace != namespace {
			continue
		}
		out = append(out, *machine.DeepCopy())
	}
	return out, nil
}

func (f *fakeClient) GetVirtualMachineClass(_ context.Context, name string) (*v1alpha3.VirtualMachineClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	class, ok := f.vmcs[name]
	if !ok {
		return nil, notFound("virtualmachineclasses", name)
	}
	out := class.DeepCopy()
	if f.onGetVMC != nil {
		f.onGetVMC(out)
	}
	return out, nil
}

func (f *fakeClient) CreateVirtualMachineClass(_ context.Context, class *v1alpha3.VirtualMachineClass) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls["vmc"]++
	if _, ok := f.vmcs[class.Name]; ok {
		return alreadyExists("virtualmachineclasses", class.Name)
	}
	f.vmcs[class.Name] = class.DeepCopy()
	return nil
}

// seedCVI inserts a CVI directly into the store (bypassing Create counters).
func (f *fakeClient) seedCVI(cvi *v1alpha2.ClusterVirtualImage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cvis[cvi.Name] = cvi.DeepCopy()
}

// seedVMClass inserts a VirtualMachineClass directly into the store.
func (f *fakeClient) seedVMClass(class *v1alpha3.VirtualMachineClass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmcs[class.Name] = class.DeepCopy()
}

func (f *fakeClient) createCount(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls[kind]
}
