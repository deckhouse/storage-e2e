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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"

	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

// dvpDiskManager implements clusterprovider.DiskManager on the DVP base
// cluster: a disk is a blank VirtualDisk in the test namespace, an attachment
// is a VirtualMachineBlockDeviceAttachment binding it to the node's VM (node
// names equal VM names — both come from ClusterDefinition hostnames).
type dvpDiskManager struct {
	virt                virtClient
	namespace           string
	defaultStorageClass string
	pollInterval        time.Duration
}

var _ clusterprovider.DiskManager = (*dvpDiskManager)(nil)

func (m *dvpDiskManager) CreateDisk(ctx context.Context, spec clusterprovider.DiskSpec) (*clusterprovider.Disk, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("create disk: name is required")
	}
	if spec.Size.Sign() <= 0 {
		return nil, fmt.Errorf("create disk %q: size must be greater than 0, got %s", spec.Name, spec.Size.String())
	}

	storageClass := spec.StorageClass
	if storageClass == "" {
		storageClass = m.defaultStorageClass
	}

	vd := &v1alpha2.VirtualDisk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: m.namespace,
			Labels:    vm.ManagedLabels(),
		},
		Spec: v1alpha2.VirtualDiskSpec{
			PersistentVolumeClaim: v1alpha2.VirtualDiskPersistentVolumeClaim{
				Size: new(spec.Size),
			},
		},
	}
	if storageClass != "" {
		sc := storageClass
		vd.Spec.PersistentVolumeClaim.StorageClass = &sc
	}

	if err := m.virt.CreateVirtualDisk(ctx, vd); err != nil {
		return nil, fmt.Errorf("create VirtualDisk %s/%s: %w", m.namespace, spec.Name, err)
	}

	var observed *v1alpha2.VirtualDisk
	err := pollObject(ctx, m.pollInterval, fmt.Sprintf("VirtualDisk %s/%s ready", m.namespace, spec.Name),
		func(ctx context.Context) (*v1alpha2.VirtualDisk, error) {
			return m.virt.GetVirtualDisk(ctx, m.namespace, spec.Name)
		},
		func(got *v1alpha2.VirtualDisk, getErr error) (bool, error) {
			if getErr != nil {
				// Transient Get errors are retried; pollObject records getErr as
				// lastErr and surfaces it if ctx is done before a Get succeeds.
				return false, nil //nolint:nilerr // intentional retry, see comment above
			}
			observed = got
			switch got.Status.Phase {
			// A blank disk on a WFFC storage class stays in WaitForFirstConsumer
			// until a VM consumes it — that is as ready as it gets before AttachDisk.
			case v1alpha2.DiskReady, v1alpha2.DiskWaitForFirstConsumer:
				return true, nil
			case v1alpha2.DiskFailed, v1alpha2.DiskLost:
				return false, fmt.Errorf("VirtualDisk %s/%s entered phase %s", m.namespace, spec.Name, got.Status.Phase)
			default:
				return false, nil
			}
		})
	if err != nil {
		return nil, err
	}

	return diskFromVirtualDisk(observed), nil
}

func (m *dvpDiskManager) DeleteDisk(ctx context.Context, diskName string) error {
	if err := m.virt.DeleteVirtualDisk(ctx, m.namespace, diskName); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualDisk %s/%s: %w", m.namespace, diskName, err)
	}

	return pollObject(ctx, m.pollInterval, fmt.Sprintf("VirtualDisk %s/%s gone", m.namespace, diskName),
		func(ctx context.Context) (*v1alpha2.VirtualDisk, error) {
			return m.virt.GetVirtualDisk(ctx, m.namespace, diskName)
		},
		func(_ *v1alpha2.VirtualDisk, getErr error) (bool, error) {
			return apierrors.IsNotFound(getErr), nil
		})
}

func (m *dvpDiskManager) AttachDisk(ctx context.Context, nodeName, diskName string) error {
	name := attachmentName(diskName, nodeName)
	vmbda := &v1alpha2.VirtualMachineBlockDeviceAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.namespace,
			Labels:    vm.ManagedLabels(),
		},
		Spec: v1alpha2.VirtualMachineBlockDeviceAttachmentSpec{
			VirtualMachineName: nodeName,
			BlockDeviceRef: v1alpha2.VMBDAObjectRef{
				Kind: v1alpha2.VMBDAObjectRefKindVirtualDisk,
				Name: diskName,
			},
		},
	}

	// AlreadyExists makes a retried attach converge on the same attachment —
	// but only after verifying the existing object actually binds this disk
	// to this VM. Blindly adopting a leftover with a colliding name would
	// report a successful attach that never happened on this node.
	if err := m.virt.CreateVMBDA(ctx, vmbda); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create VirtualMachineBlockDeviceAttachment %s/%s: %w", m.namespace, name, err)
		}
		existing, getErr := m.virt.GetVMBDA(ctx, m.namespace, name)
		if getErr != nil {
			return fmt.Errorf("get existing VirtualMachineBlockDeviceAttachment %s/%s: %w", m.namespace, name, getErr)
		}
		if existing.Spec.VirtualMachineName != nodeName ||
			existing.Spec.BlockDeviceRef.Kind != v1alpha2.VMBDAObjectRefKindVirtualDisk ||
			existing.Spec.BlockDeviceRef.Name != diskName {
			return fmt.Errorf("attachment %s/%s already exists but binds %s %q to VM %q, not %s %q to VM %q",
				m.namespace, name,
				existing.Spec.BlockDeviceRef.Kind, existing.Spec.BlockDeviceRef.Name, existing.Spec.VirtualMachineName,
				v1alpha2.VMBDAObjectRefKindVirtualDisk, diskName, nodeName)
		}
	}

	return pollObject(ctx, m.pollInterval, fmt.Sprintf("attachment %s/%s attached", m.namespace, name),
		func(ctx context.Context) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
			return m.virt.GetVMBDA(ctx, m.namespace, name)
		},
		func(got *v1alpha2.VirtualMachineBlockDeviceAttachment, getErr error) (bool, error) {
			if getErr != nil {
				// Transient Get errors are retried; pollObject records getErr as
				// lastErr and surfaces it if ctx is done before a Get succeeds.
				return false, nil //nolint:nilerr // intentional retry, see comment above
			}
			switch got.Status.Phase {
			case v1alpha2.BlockDeviceAttachmentPhaseAttached:
				return true, nil
			case v1alpha2.BlockDeviceAttachmentPhaseFailed:
				return false, fmt.Errorf("attachment %s/%s entered phase %s", m.namespace, name, got.Status.Phase)
			default:
				return false, nil
			}
		})
}

func (m *dvpDiskManager) DetachDisk(ctx context.Context, nodeName, diskName string) error {
	name := attachmentName(diskName, nodeName)
	if err := m.virt.DeleteVMBDA(ctx, m.namespace, name); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualMachineBlockDeviceAttachment %s/%s: %w", m.namespace, name, err)
	}

	return pollObject(ctx, m.pollInterval, fmt.Sprintf("attachment %s/%s gone", m.namespace, name),
		func(ctx context.Context) (*v1alpha2.VirtualMachineBlockDeviceAttachment, error) {
			return m.virt.GetVMBDA(ctx, m.namespace, name)
		},
		func(_ *v1alpha2.VirtualMachineBlockDeviceAttachment, getErr error) (bool, error) {
			return apierrors.IsNotFound(getErr), nil
		})
}

func (m *dvpDiskManager) ResizeDisk(ctx context.Context, diskName string, newSize resource.Quantity) error {
	if diskName == "" {
		return fmt.Errorf("resize disk: name is required")
	}
	if newSize.Sign() <= 0 {
		return fmt.Errorf("resize disk %q: size must be greater than 0, got %s", diskName, newSize.String())
	}

	vd, err := m.virt.GetVirtualDisk(ctx, m.namespace, diskName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("resize disk %q: VirtualDisk %s/%s not found: %w", diskName, m.namespace, diskName, err)
		}
		return fmt.Errorf("resize disk %q: get VirtualDisk %s/%s: %w", diskName, m.namespace, diskName, err)
	}

	// PVC expansion can only grow a volume: reject shrink outright and treat an
	// equal size as a no-op success (nothing to apply, so no Update and no wait).
	// A nil Spec size means the size is controller-derived (e.g. from a
	// DataSource); we cannot validate growth against it, so we set the desired
	// size explicitly and let the poll confirm it applied.
	if cur := vd.Spec.PersistentVolumeClaim.Size; cur != nil {
		switch newSize.Cmp(*cur) {
		case 0:
			return nil
		case -1:
			return fmt.Errorf("resize disk %q: cannot shrink from %s to %s (PVC expansion only grows)",
				diskName, cur.String(), newSize.String())
		}
	}

	vd.Spec.PersistentVolumeClaim.Size = new(newSize)
	if err := m.virt.UpdateVirtualDisk(ctx, vd); err != nil {
		return fmt.Errorf("resize disk %q: update VirtualDisk %s/%s: %w", diskName, m.namespace, diskName, err)
	}

	return pollObject(ctx, m.pollInterval, fmt.Sprintf("VirtualDisk %s/%s resized to %s", m.namespace, diskName, newSize.String()),
		func(ctx context.Context) (*v1alpha2.VirtualDisk, error) {
			return m.virt.GetVirtualDisk(ctx, m.namespace, diskName)
		},
		func(got *v1alpha2.VirtualDisk, getErr error) (bool, error) {
			if getErr != nil {
				// Transient Get errors are retried; pollObject records getErr as
				// lastErr and surfaces it if ctx is done before a Get succeeds.
				return false, nil //nolint:nilerr // intentional retry, see comment above
			}
			switch got.Status.Phase {
			case v1alpha2.DiskFailed, v1alpha2.DiskLost:
				return false, fmt.Errorf("VirtualDisk %s/%s entered phase %s", m.namespace, diskName, got.Status.Phase)
			// A blank disk on a WFFC storage class has no PVC yet, so it never
			// leaves WaitForFirstConsumer before a VM consumes it — the desired
			// size is recorded and will apply on first use, so treat it as done.
			case v1alpha2.DiskReady, v1alpha2.DiskWaitForFirstConsumer:
				return resizeApplied(got, newSize), nil
			default:
				// Pending/Provisioning/Resizing: keep waiting for the disk to
				// settle on the new capacity.
				return false, nil
			}
		})
}

// resizeApplied reports whether vd reflects at least newSize. Status.Capacity
// is the authoritative "requested PVC capacity" the controller converged on
// (a human-readable string like "50G"); when it is set and parseable it wins.
// Otherwise we fall back to the desired Spec size we just wrote, which is a
// best-effort signal (see the API note that Status.Capacity may lag).
func resizeApplied(vd *v1alpha2.VirtualDisk, newSize resource.Quantity) bool {
	if vd.Status.Capacity != "" {
		if got, err := resource.ParseQuantity(vd.Status.Capacity); err == nil {
			return got.Cmp(newSize) >= 0
		}
	}
	if size := vd.Spec.PersistentVolumeClaim.Size; size != nil {
		return size.Cmp(newSize) >= 0
	}
	return false
}

func diskFromVirtualDisk(vd *v1alpha2.VirtualDisk) *clusterprovider.Disk {
	d := &clusterprovider.Disk{
		Name:  vd.Name,
		Phase: string(vd.Status.Phase),
	}
	if vd.Spec.PersistentVolumeClaim.Size != nil {
		d.Size = *vd.Spec.PersistentVolumeClaim.Size
	}
	if vd.Spec.PersistentVolumeClaim.StorageClass != nil {
		d.StorageClass = *vd.Spec.PersistentVolumeClaim.StorageClass
	}
	return d
}

const (
	attachmentNameMaxLen = 63 // DNS-1123 name limit
	attachmentHashLen    = 8
)

// attachmentName derives the deterministic VMBDA name for a disk-node pair, so
// DetachDisk finds what AttachDisk created without extra state. Long pairs are
// truncated with a hash suffix for uniqueness.
func attachmentName(diskName, nodeName string) string {
	name := diskName + "-" + nodeName
	if len(name) <= attachmentNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:attachmentHashLen]
	base := strings.TrimRight(name[:attachmentNameMaxLen-attachmentHashLen-1], "-")
	return base + "-" + hash
}

// pollObject re-reads an object every interval until cond reports the awaited
// state (or a terminal failure). Get errors go to cond too, so it can treat
// NotFound as the goal; ignored ones keep the poll going and surface alongside
// the ctx deadline error.
func pollObject[T any](
	ctx context.Context,
	interval time.Duration,
	what string,
	get func(ctx context.Context) (T, error),
	cond func(obj T, getErr error) (done bool, err error),
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for {
		obj, getErr := get(ctx)
		if getErr != nil {
			lastErr = getErr
		}
		done, err := cond(obj, getErr)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("waiting for %s: %w (last error: %w)", what, ctx.Err(), lastErr)
			}
			return fmt.Errorf("waiting for %s: %w", what, ctx.Err())
		case <-ticker.C:
		}
	}
}
