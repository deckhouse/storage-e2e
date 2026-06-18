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
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

func TestBuildClusterVirtualImage(t *testing.T) {
	labels := managedLabels("run-1")
	cvi := buildClusterVirtualImage("ubuntu", "http://example/img.qcow2", labels)

	if cvi.Name != "ubuntu" {
		t.Errorf("Name = %q, want ubuntu", cvi.Name)
	}
	if cvi.Spec.DataSource.Type != v1alpha2.DataSourceTypeHTTP {
		t.Errorf("DataSource.Type = %q, want HTTP", cvi.Spec.DataSource.Type)
	}
	if cvi.Spec.DataSource.HTTP == nil || cvi.Spec.DataSource.HTTP.URL != "http://example/img.qcow2" {
		t.Errorf("DataSource.HTTP = %+v, want URL set", cvi.Spec.DataSource.HTTP)
	}
	if cvi.Labels[managedByLabelKey] != managedByLabelValue || cvi.Labels[runLabelKey] != "run-1" {
		t.Errorf("labels = %v, want managed labels for run-1", cvi.Labels)
	}
}

func TestBuildVirtualDisk(t *testing.T) {
	tests := []struct {
		name         string
		diskSizeGi   int
		storageClass string
		wantErr      bool
		wantBytes    int64
		wantSCSet    bool
	}{
		{name: "valid with storage class", diskSizeGi: 20, storageClass: "fast", wantBytes: 20 * 1024 * 1024 * 1024, wantSCSet: true},
		{name: "valid without storage class", diskSizeGi: 1, storageClass: "", wantBytes: 1 * 1024 * 1024 * 1024},
		{name: "zero size", diskSizeGi: 0, wantErr: true},
		{name: "negative size", diskSizeGi: -5, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vd, err := buildVirtualDisk("ns", "vm-system", "ubuntu", tt.storageClass, tt.diskSizeGi, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if vd.Spec.PersistentVolumeClaim.Size.Value() != tt.wantBytes {
				t.Errorf("size = %d bytes, want %d", vd.Spec.PersistentVolumeClaim.Size.Value(), tt.wantBytes)
			}
			if tt.wantSCSet {
				if vd.Spec.PersistentVolumeClaim.StorageClass == nil || *vd.Spec.PersistentVolumeClaim.StorageClass != tt.storageClass {
					t.Errorf("storageClass = %v, want %q", vd.Spec.PersistentVolumeClaim.StorageClass, tt.storageClass)
				}
			} else if vd.Spec.PersistentVolumeClaim.StorageClass != nil {
				t.Errorf("storageClass = %v, want nil", *vd.Spec.PersistentVolumeClaim.StorageClass)
			}
			if vd.Spec.DataSource.Type != v1alpha2.DataSourceTypeObjectRef {
				t.Errorf("dataSource type = %q, want ObjectRef", vd.Spec.DataSource.Type)
			}
			if vd.Spec.DataSource.ObjectRef.Kind != v1alpha2.VirtualDiskObjectRefKindClusterVirtualImage {
				t.Errorf("objectRef kind = %q, want ClusterVirtualImage", vd.Spec.DataSource.ObjectRef.Kind)
			}
			if vd.Spec.DataSource.ObjectRef.Name != "ubuntu" {
				t.Errorf("objectRef name = %q, want ubuntu", vd.Spec.DataSource.ObjectRef.Name)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestBuildVirtualMachine(t *testing.T) {
	tests := []struct {
		name             string
		params           vmParams
		wantErr          bool
		wantCoreFraction string
		wantMemoryStr    string
	}{
		{
			name:             "default core fraction",
			params:           vmParams{Name: "m1", Namespace: "ns", VMClassName: "generic", DiskName: "m1-system", CPU: 4, RAMGi: 8},
			wantCoreFraction: "100%",
			wantMemoryStr:    "8Gi",
		},
		{
			name:             "custom core fraction",
			params:           vmParams{Name: "m2", Namespace: "ns", VMClassName: "generic", DiskName: "m2-system", CPU: 2, RAMGi: 4, CoreFraction: intPtr(50)},
			wantCoreFraction: "50%",
			wantMemoryStr:    "4Gi",
		},
		{name: "zero cpu", params: vmParams{Name: "m3", CPU: 0, RAMGi: 4}, wantErr: true},
		{name: "zero ram", params: vmParams{Name: "m4", CPU: 2, RAMGi: 0}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine, err := buildVirtualMachine(tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if machine.Spec.CPU.Cores != tt.params.CPU {
				t.Errorf("cores = %d, want %d", machine.Spec.CPU.Cores, tt.params.CPU)
			}
			if machine.Spec.CPU.CoreFraction != tt.wantCoreFraction {
				t.Errorf("coreFraction = %q, want %q", machine.Spec.CPU.CoreFraction, tt.wantCoreFraction)
			}
			if machine.Spec.Memory.Size.String() != tt.wantMemoryStr {
				t.Errorf("memory = %q, want %q", machine.Spec.Memory.Size.String(), tt.wantMemoryStr)
			}
			if machine.Spec.RunPolicy != v1alpha2.AlwaysOnPolicy {
				t.Errorf("runPolicy = %q, want AlwaysOn", machine.Spec.RunPolicy)
			}
			if machine.Spec.OsType != v1alpha2.GenericOs {
				t.Errorf("osType = %q, want Generic", machine.Spec.OsType)
			}
			if machine.Spec.Bootloader != v1alpha2.BIOS {
				t.Errorf("bootloader = %q, want BIOS", machine.Spec.Bootloader)
			}
			if machine.Spec.Provisioning == nil || machine.Spec.Provisioning.Type != v1alpha2.ProvisioningTypeUserData {
				t.Errorf("provisioning = %+v, want UserData", machine.Spec.Provisioning)
			}
			if len(machine.Spec.BlockDeviceRefs) != 1 ||
				machine.Spec.BlockDeviceRefs[0].Kind != v1alpha2.DiskDevice ||
				machine.Spec.BlockDeviceRefs[0].Name != tt.params.DiskName {
				t.Errorf("blockDeviceRefs = %+v, want one VirtualDisk %q", machine.Spec.BlockDeviceRefs, tt.params.DiskName)
			}
		})
	}
}

func TestBuildVirtualMachineClass(t *testing.T) {
	template := v1alpha3.VirtualMachineClassSpec{
		NodeSelector: v1alpha3.NodeSelector{MatchLabels: map[string]string{"node": "x"}},
		Tolerations:  []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}},
		CPU:          v1alpha3.CPU{Type: v1alpha3.CPUTypeModel, Model: "IvyBridge"},
	}

	class := buildVirtualMachineClass("custom", template, managedLabels("run-1"))

	if class.Spec.CPU.Type != v1alpha3.CPUTypeHost {
		t.Errorf("cpu.type = %q, want Host", class.Spec.CPU.Type)
	}
	if class.Spec.CPU.Model != "" {
		t.Errorf("cpu.model = %q, want empty (Host has no model)", class.Spec.CPU.Model)
	}
	if len(class.Spec.NodeSelector.MatchLabels) != 0 {
		t.Errorf("nodeSelector = %v, want cleared", class.Spec.NodeSelector)
	}
	if class.Spec.Tolerations != nil {
		t.Errorf("tolerations = %v, want nil", class.Spec.Tolerations)
	}
	if class.Labels[runLabelKey] != "run-1" {
		t.Errorf("labels = %v, want run-1", class.Labels)
	}
}
