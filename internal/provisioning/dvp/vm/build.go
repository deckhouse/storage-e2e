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
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

const bytesPerGiB = int64(1024) * 1024 * 1024

const defaultCoreFraction = "100%"

func buildClusterVirtualImage(name, imageURL string, labels map[string]string) *v1alpha2.ClusterVirtualImage {
	return &v1alpha2.ClusterVirtualImage{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: v1alpha2.ClusterVirtualImageSpec{
			DataSource: v1alpha2.ClusterVirtualImageDataSource{
				Type: v1alpha2.DataSourceTypeHTTP,
				HTTP: &v1alpha2.DataSourceHTTP{
					URL: imageURL,
				},
			},
		},
	}
}

func buildVirtualDisk(namespace, name, cviName, storageClass string, diskSizeGi int, labels map[string]string) (*v1alpha2.VirtualDisk, error) {
	if diskSizeGi <= 0 {
		return nil, fmt.Errorf("disk size must be greater than 0, got %d", diskSizeGi)
	}

	size := resource.NewQuantity(int64(diskSizeGi)*bytesPerGiB, resource.BinarySI)

	vd := &v1alpha2.VirtualDisk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: v1alpha2.VirtualDiskSpec{
			PersistentVolumeClaim: v1alpha2.VirtualDiskPersistentVolumeClaim{
				Size: size,
			},
			DataSource: &v1alpha2.VirtualDiskDataSource{
				Type: v1alpha2.DataSourceTypeObjectRef,
				ObjectRef: &v1alpha2.VirtualDiskObjectRef{
					Kind: v1alpha2.VirtualDiskObjectRefKindClusterVirtualImage,
					Name: cviName,
				},
			},
		},
	}
	if storageClass != "" {
		sc := storageClass
		vd.Spec.PersistentVolumeClaim.StorageClass = &sc
	}
	return vd, nil
}

type vmParams struct {
	Namespace    string
	Name         string
	VMClassName  string
	DiskName     string
	CloudInit    string
	CPU          int
	RAMGi        int
	CoreFraction *int
	Labels       map[string]string
}

func buildVirtualMachine(p vmParams) (*v1alpha2.VirtualMachine, error) {
	if p.CPU <= 0 {
		return nil, fmt.Errorf("vm %q: cpu must be greater than 0, got %d", p.Name, p.CPU)
	}
	if p.RAMGi <= 0 {
		return nil, fmt.Errorf("vm %q: ram must be greater than 0, got %d", p.Name, p.RAMGi)
	}

	memory, err := resource.ParseQuantity(fmt.Sprintf("%dGi", p.RAMGi))
	if err != nil {
		return nil, fmt.Errorf("vm %q: parse memory quantity: %w", p.Name, err)
	}

	coreFraction := defaultCoreFraction
	if p.CoreFraction != nil {
		coreFraction = fmt.Sprintf("%d%%", *p.CoreFraction)
	}

	return &v1alpha2.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			Labels:    p.Labels,
		},
		Spec: v1alpha2.VirtualMachineSpec{
			VirtualMachineClassName:  p.VMClassName,
			EnableParavirtualization: true,
			RunPolicy:                v1alpha2.AlwaysOnPolicy,
			OsType:                   v1alpha2.GenericOs,
			Bootloader:               v1alpha2.BIOS,
			LiveMigrationPolicy:      v1alpha2.PreferSafeMigrationPolicy,
			CPU: v1alpha2.CPUSpec{
				Cores:        p.CPU,
				CoreFraction: coreFraction,
			},
			Memory: v1alpha2.MemorySpec{
				Size: memory,
			},
			BlockDeviceRefs: []v1alpha2.BlockDeviceSpecRef{
				{
					Kind: v1alpha2.DiskDevice,
					Name: p.DiskName,
				},
			},
			Provisioning: &v1alpha2.Provisioning{
				Type:     v1alpha2.ProvisioningTypeUserData,
				UserData: p.CloudInit,
			},
		},
	}, nil
}

func buildVirtualMachineClass(name string, template v1alpha3.VirtualMachineClassSpec, labels map[string]string) *v1alpha3.VirtualMachineClass {
	spec := *template.DeepCopy()
	spec.CPU = v1alpha3.CPU{Type: v1alpha3.CPUTypeHost}
	spec.NodeSelector = v1alpha3.NodeSelector{}
	spec.Tolerations = nil

	return &v1alpha3.VirtualMachineClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: spec,
	}
}
