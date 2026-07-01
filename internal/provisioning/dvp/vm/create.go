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
	"github.com/deckhouse/virtualization/api/core/v1alpha2/vdcondition"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

func ignoreAlreadyExists(err error) error {
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func createIfAbsentClusterVirtualImage(ctx context.Context, c Client, cvi *v1alpha2.ClusterVirtualImage) error {
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

func createIfAbsentVirtualDisk(ctx context.Context, c Client, vd *v1alpha2.VirtualDisk) error {
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

func createIfAbsentVirtualMachine(ctx context.Context, c Client, machine *v1alpha2.VirtualMachine) error {
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

func createIfAbsentVirtualMachineClass(ctx context.Context, c Client, class *v1alpha3.VirtualMachineClass) error {
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

func terminalGetErr(err error) bool {
	return apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) ||
		apierrors.IsBadRequest(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsMethodNotSupported(err)
}

func clusterVirtualImageReady(cvi *v1alpha2.ClusterVirtualImage, getErr error) (bool, error) {
	if getErr != nil {
		if terminalGetErr(getErr) {
			return false, getErr
		}
		return false, nil
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

func virtualMachineClassReady(class *v1alpha3.VirtualMachineClass, getErr error) (bool, error) {
	if getErr != nil {
		if terminalGetErr(getErr) {
			return false, getErr
		}
		return false, nil
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

func virtualMachineRunning(machine *v1alpha2.VirtualMachine, getErr error) (bool, error) {
	if getErr != nil {
		if terminalGetErr(getErr) {
			return false, getErr
		}
		return false, nil
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

// virtualDiskFailure reports whether the disk sits in a failure phase
// (Failed or PVCLost) and returns the reason/message from its Ready condition,
// if present, for logging. It intentionally does not treat this as a hard
// error: DVCR/registry outages are often transient, so callers keep waiting.
func virtualDiskFailure(vd *v1alpha2.VirtualDisk) (failed bool, reason, message string) {
	if vd == nil {
		return false, "", ""
	}
	switch vd.Status.Phase {
	case v1alpha2.DiskFailed, v1alpha2.DiskLost:
	default:
		return false, "", ""
	}
	for _, c := range vd.Status.Conditions {
		if c.Type == string(vdcondition.ReadyType) {
			return true, c.Reason, c.Message
		}
	}
	return true, "", ""
}

func resourceDeleted[T any](_ T, getErr error) (bool, error) {
	if apierrors.IsNotFound(getErr) {
		return true, nil
	}
	return false, nil
}
