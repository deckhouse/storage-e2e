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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

const (
	// diskNodeLabelKey lets List filter disks by node without parsing names.
	diskNodeLabelKey = "storage-e2e.deckhouse.io/attached-to"

	diskAttachPollInterval = 5 * time.Second
	diskAttachTimeout      = 10 * time.Minute
	diskDetachTimeout      = 5 * time.Minute
)

// dvpDiskManager labels everything it creates with the framework's managed-by
// label, so Provider.Remove sweeps leftovers even after a crashed test run.
type dvpDiskManager struct {
	virtClient          *virtualization.Client
	namespace           string
	defaultStorageClass string
	logger              *slog.Logger
}

var _ clusterprovider.DiskManager = (*dvpDiskManager)(nil)

func attachmentName(diskName string) string { return diskName + "-attachment" }

func (m *dvpDiskManager) diskLabels(nodeName string) map[string]string {
	labels := vm.ManagedLabels()
	labels[diskNodeLabelKey] = nodeName
	return labels
}

func (m *dvpDiskManager) Attach(ctx context.Context, spec clusterprovider.DiskSpec) (*clusterprovider.Disk, error) {
	if spec.NodeName == "" {
		return nil, fmt.Errorf("disk spec: NodeName is required")
	}
	if spec.Size.IsZero() {
		return nil, fmt.Errorf("disk spec: Size is required")
	}

	diskName := spec.Name
	if diskName == "" {
		suffix := make([]byte, 3)
		if _, err := rand.Read(suffix); err != nil {
			return nil, fmt.Errorf("generate disk name: %w", err)
		}
		diskName = fmt.Sprintf("%s-e2e-disk-%s", spec.NodeName, hex.EncodeToString(suffix))
	}

	storageClass := spec.StorageClass
	if storageClass == "" {
		storageClass = m.defaultStorageClass
	}
	var storageClassRef *string
	if storageClass != "" {
		storageClassRef = &storageClass
	}

	size := spec.Size
	disk := &v1alpha2.VirtualDisk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      diskName,
			Namespace: m.namespace,
			Labels:    m.diskLabels(spec.NodeName),
		},
		Spec: v1alpha2.VirtualDiskSpec{
			PersistentVolumeClaim: v1alpha2.VirtualDiskPersistentVolumeClaim{
				Size:         &size,
				StorageClass: storageClassRef,
			},
		},
	}
	if err := m.virtClient.VirtualDisks().Create(ctx, disk); err != nil {
		return nil, fmt.Errorf("create VirtualDisk %s/%s: %w", m.namespace, diskName, err)
	}

	attachment := &v1alpha2.VirtualMachineBlockDeviceAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      attachmentName(diskName),
			Namespace: m.namespace,
			Labels:    m.diskLabels(spec.NodeName),
		},
		Spec: v1alpha2.VirtualMachineBlockDeviceAttachmentSpec{
			VirtualMachineName: spec.NodeName,
			BlockDeviceRef: v1alpha2.VMBDAObjectRef{
				Kind: v1alpha2.VMBDAObjectRefKindVirtualDisk,
				Name: diskName,
			},
		},
	}
	if err := m.virtClient.VirtualMachineBlockDeviceAttachments().Create(ctx, attachment); err != nil {
		return nil, fmt.Errorf("create VirtualMachineBlockDeviceAttachment %s/%s: %w",
			m.namespace, attachment.Name, err)
	}

	if err := m.waitAttached(ctx, attachment.Name); err != nil {
		return nil, err
	}

	m.logger.Info("disk attached", "disk", diskName, "node", spec.NodeName, "size", size.String())
	return &clusterprovider.Disk{
		Name:         diskName,
		NodeName:     spec.NodeName,
		StorageClass: storageClass,
		Size:         size,
	}, nil
}

func (m *dvpDiskManager) waitAttached(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, diskAttachTimeout)
	defer cancel()

	ticker := time.NewTicker(diskAttachPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for attachment %s/%s: %w", m.namespace, name, ctx.Err())
		case <-ticker.C:
			attachment, err := m.virtClient.VirtualMachineBlockDeviceAttachments().Get(ctx, m.namespace, name)
			if err != nil {
				m.logger.Warn("get attachment failed, retrying", "attachment", name, "err", err)
				continue
			}
			switch attachment.Status.Phase {
			case v1alpha2.BlockDeviceAttachmentPhaseAttached:
				return nil
			case v1alpha2.BlockDeviceAttachmentPhaseFailed:
				return fmt.Errorf("attachment %s/%s is in Failed phase", m.namespace, name)
			}
		}
	}
}

func (m *dvpDiskManager) Detach(ctx context.Context, ref clusterprovider.DiskRef) error {
	if ref.Name == "" {
		return fmt.Errorf("disk ref: Name is required")
	}

	err := m.virtClient.VirtualMachineBlockDeviceAttachments().Delete(ctx, m.namespace, attachmentName(ref.Name))
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete attachment for disk %s: %w", ref.Name, err)
	}

	err = m.virtClient.VirtualDisks().Delete(ctx, m.namespace, ref.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualDisk %s: %w", ref.Name, err)
	}

	if err := m.waitDiskGone(ctx, ref.Name); err != nil {
		return err
	}
	m.logger.Info("disk detached", "disk", ref.Name, "node", ref.NodeName)
	return nil
}

func (m *dvpDiskManager) waitDiskGone(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, diskDetachTimeout)
	defer cancel()

	ticker := time.NewTicker(diskAttachPollInterval)
	defer ticker.Stop()
	for {
		_, err := m.virtClient.VirtualDisks().Get(ctx, m.namespace, name)
		if apierrors.IsNotFound(err) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for VirtualDisk %s/%s deletion: %w", m.namespace, name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *dvpDiskManager) List(ctx context.Context, nodeName string) ([]clusterprovider.Disk, error) {
	disks, err := m.virtClient.VirtualDisks().List(ctx, m.namespace)
	if err != nil {
		return nil, fmt.Errorf("list VirtualDisks in %s: %w", m.namespace, err)
	}

	var out []clusterprovider.Disk
	for i := range disks {
		vd := &disks[i]
		if vd.Labels[vm.ManagedByLabelKey] != vm.ManagedByLabelValue {
			continue
		}
		if vd.Labels[diskNodeLabelKey] != nodeName {
			continue
		}
		disk := clusterprovider.Disk{
			Name:     vd.Name,
			NodeName: nodeName,
		}
		if vd.Spec.PersistentVolumeClaim.Size != nil {
			disk.Size = *vd.Spec.PersistentVolumeClaim.Size
		}
		if vd.Spec.PersistentVolumeClaim.StorageClass != nil {
			disk.StorageClass = *vd.Spec.PersistentVolumeClaim.StorageClass
		}
		out = append(out, disk)
	}
	return out, nil
}
