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

	vms map[string]*v1alpha2.VirtualMachine

	// Injected errors (nil = success).
	getVMErr error
}

func newFakeVirt() *fakeVirt {
	return &fakeVirt{
		vms: map[string]*v1alpha2.VirtualMachine{},
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

// Compile-time guarantee the fake satisfies the seam.
var _ virtClient = (*fakeVirt)(nil)
