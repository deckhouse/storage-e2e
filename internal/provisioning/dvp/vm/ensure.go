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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

// ignoreAlreadyExists treats an AlreadyExists error as success. Every ensure
// function uses it so that "already there" is handled uniformly across all
// resource types: two concurrent runs (or a retried run) converge instead of
// failing.
func ignoreAlreadyExists(err error) error {
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// ensureClusterVirtualImage creates the CVI if it does not exist. An existing
// CVI (matched by name) is left untouched, which is what we want: the image is
// cluster-scoped and may be shared by other runs.
func ensureClusterVirtualImage(ctx context.Context, c Client, cvi *v1alpha2.ClusterVirtualImage) error {
	_, err := c.GetClusterVirtualImage(ctx, cvi.Name)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get ClusterVirtualImage %q: %w", cvi.Name, err)
	}
	if createErr := ignoreAlreadyExists(c.CreateClusterVirtualImage(ctx, cvi)); createErr != nil {
		return fmt.Errorf("create ClusterVirtualImage %q: %w", cvi.Name, createErr)
	}
	return nil
}

func ensureVirtualDisk(ctx context.Context, c Client, vd *v1alpha2.VirtualDisk) error {
	_, err := c.GetVirtualDisk(ctx, vd.Namespace, vd.Name)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get VirtualDisk %s/%s: %w", vd.Namespace, vd.Name, err)
	}
	if createErr := ignoreAlreadyExists(c.CreateVirtualDisk(ctx, vd)); createErr != nil {
		return fmt.Errorf("create VirtualDisk %s/%s: %w", vd.Namespace, vd.Name, createErr)
	}
	return nil
}

// ensureVirtualMachine creates the VirtualMachine if it does not exist.
func ensureVirtualMachine(ctx context.Context, c Client, machine *v1alpha2.VirtualMachine) error {
	_, err := c.GetVirtualMachine(ctx, machine.Namespace, machine.Name)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get VirtualMachine %s/%s: %w", machine.Namespace, machine.Name, err)
	}
	if createErr := ignoreAlreadyExists(c.CreateVirtualMachine(ctx, machine)); createErr != nil {
		return fmt.Errorf("create VirtualMachine %s/%s: %w", machine.Namespace, machine.Name, createErr)
	}
	return nil
}

// ensureVirtualMachineClass creates the VirtualMachineClass if it does not
// exist.
func ensureVirtualMachineClass(ctx context.Context, c Client, class *v1alpha3.VirtualMachineClass) error {
	_, err := c.GetVirtualMachineClass(ctx, class.Name)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get VirtualMachineClass %q: %w", class.Name, err)
	}
	if createErr := ignoreAlreadyExists(c.CreateVirtualMachineClass(ctx, class)); createErr != nil {
		return fmt.Errorf("create VirtualMachineClass %q: %w", class.Name, createErr)
	}
	return nil
}

// clusterVirtualImageReady is the readiness condition for a CVI.
func clusterVirtualImageReady(cvi *v1alpha2.ClusterVirtualImage, getErr error) (bool, error) {
	if getErr != nil {
		return false, fmt.Errorf("get ClusterVirtualImage: %w", getErr)
	}
	switch cvi.Status.Phase {
	case v1alpha2.ImageReady:
		return true, nil
	case v1alpha2.ImageFailed, v1alpha2.ImageLost:
		return false, fmt.Errorf("ClusterVirtualImage %q reached phase %s", cvi.Name, cvi.Status.Phase)
	default:
		return false, nil
	}
}

// virtualMachineClassReady is the readiness condition for a VirtualMachineClass.
func virtualMachineClassReady(class *v1alpha3.VirtualMachineClass, getErr error) (bool, error) {
	if getErr != nil {
		return false, fmt.Errorf("get VirtualMachineClass: %w", getErr)
	}
	switch class.Status.Phase {
	case v1alpha3.ClassPhaseReady:
		return true, nil
	case v1alpha3.ClassPhaseTerminating:
		return false, fmt.Errorf("VirtualMachineClass %q is terminating", class.Name)
	default:
		return false, nil
	}
}

// virtualMachineRunning reports done only when the VM is Running and has an IP
// address: the hypervisor publishes the IP via the guest agent shortly after
// the VM enters Running, so we keep polling until both hold.
func virtualMachineRunning(machine *v1alpha2.VirtualMachine, getErr error) (bool, error) {
	if getErr != nil {
		return false, fmt.Errorf("get VirtualMachine: %w", getErr)
	}
	switch machine.Status.Phase {
	case v1alpha2.MachineRunning:
		return machine.Status.IPAddress != "", nil
	case v1alpha2.MachineDegraded:
		return false, fmt.Errorf("VirtualMachine %q is degraded", machine.Name)
	default:
		return false, nil
	}
}

// resourceDeleted is the condition for any get-by-name wait that should finish
// once the object is gone. It is generic so it works for VMs, disks and images.
func resourceDeleted[T any](_ T, getErr error) (bool, error) {
	if apierrors.IsNotFound(getErr) {
		return true, nil
	}
	if getErr != nil {
		return false, getErr
	}
	return false, nil
}
