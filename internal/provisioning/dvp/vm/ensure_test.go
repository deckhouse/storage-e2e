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
	"testing"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

func TestEnsureClusterVirtualImageIdempotent(t *testing.T) {
	c := newFakeClient()
	cvi := buildClusterVirtualImage("ubuntu", "http://example/img.qcow2", managedLabels())

	for i := 0; i < 3; i++ {
		if err := ensureClusterVirtualImage(context.Background(), c, cvi); err != nil {
			t.Fatalf("ensure #%d: %v", i, err)
		}
	}
	if got := c.createCount("cvi"); got != 1 {
		t.Errorf("create called %d times, want 1 (idempotent)", got)
	}
}

func TestEnsureVirtualMachineIdempotent(t *testing.T) {
	c := newFakeClient()
	machine, err := buildVirtualMachine(vmParams{Name: "m1", Namespace: "ns", VMClassName: "generic", DiskName: "m1-system", CPU: 2, RAMGi: 4})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := ensureVirtualMachine(context.Background(), c, machine); err != nil {
			t.Fatalf("ensure #%d: %v", i, err)
		}
	}
	if got := c.createCount("vm"); got != 1 {
		t.Errorf("create called %d times, want 1", got)
	}
}

func TestClusterVirtualImageReadyCondition(t *testing.T) {
	tests := []struct {
		phase    v1alpha2.ImagePhase
		wantDone bool
		wantErr  bool
	}{
		{v1alpha2.ImageReady, true, false},
		{v1alpha2.ImageProvisioning, false, false},
		{v1alpha2.ImagePending, false, false},
		{v1alpha2.ImageFailed, false, true},
		{v1alpha2.ImageLost, false, true},
	}
	for _, tt := range tests {
		cvi := &v1alpha2.ClusterVirtualImage{}
		cvi.Status.Phase = tt.phase
		done, err := clusterVirtualImageReady(cvi, nil)
		if done != tt.wantDone || (err != nil) != tt.wantErr {
			t.Errorf("phase %s: done=%v err=%v, want done=%v err=%v", tt.phase, done, err, tt.wantDone, tt.wantErr)
		}
	}
}

func TestVirtualMachineRunningCondition(t *testing.T) {
	tests := []struct {
		name     string
		phase    v1alpha2.MachinePhase
		ip       string
		wantDone bool
		wantErr  bool
	}{
		{"running with ip", v1alpha2.MachineRunning, "10.0.0.1", true, false},
		{"running no ip", v1alpha2.MachineRunning, "", false, false},
		{"pending", v1alpha2.MachinePending, "", false, false},
		{"degraded", v1alpha2.MachineDegraded, "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := &v1alpha2.VirtualMachine{}
			machine.Status.Phase = tt.phase
			machine.Status.IPAddress = tt.ip
			done, err := virtualMachineRunning(machine, nil)
			if done != tt.wantDone || (err != nil) != tt.wantErr {
				t.Errorf("done=%v err=%v, want done=%v err=%v", done, err, tt.wantDone, tt.wantErr)
			}
		})
	}
}

func TestVirtualMachineClassReadyCondition(t *testing.T) {
	tests := []struct {
		phase    v1alpha3.VirtualMachineClassPhase
		wantDone bool
		wantErr  bool
	}{
		{v1alpha3.ClassPhaseReady, true, false},
		{v1alpha3.ClassPhasePending, false, false},
		{v1alpha3.ClassPhaseTerminating, false, true},
	}
	for _, tt := range tests {
		class := &v1alpha3.VirtualMachineClass{}
		class.Status.Phase = tt.phase
		done, err := virtualMachineClassReady(class, nil)
		if done != tt.wantDone || (err != nil) != tt.wantErr {
			t.Errorf("phase %s: done=%v err=%v, want done=%v err=%v", tt.phase, done, err, tt.wantDone, tt.wantErr)
		}
	}
}

func TestResourceDeletedCondition(t *testing.T) {
	if done, err := resourceDeleted[*v1alpha2.VirtualMachine](nil, notFound("virtualmachines", "x")); !done || err != nil {
		t.Errorf("notfound: done=%v err=%v, want done=true err=nil", done, err)
	}
	if done, err := resourceDeleted[*v1alpha2.VirtualMachine](&v1alpha2.VirtualMachine{}, nil); done || err != nil {
		t.Errorf("exists: done=%v err=%v, want done=false err=nil", done, err)
	}
}
