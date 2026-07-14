/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dvp

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"

	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

const testDisksNamespace = "e2e-tests"

func newTestDiskManager(fake *fakeVirt) *dvpDiskManager {
	return &dvpDiskManager{
		virt:                fake,
		namespace:           testDisksNamespace,
		defaultStorageClass: "default-sc",
		pollInterval:        time.Millisecond,
	}
}

func TestCreateDiskWaitsForReadyAndAppliesDefaults(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskReady
	}
	m := newTestDiskManager(fake)

	disk, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{
		Name: "extra-disk",
		Size: resource.MustParse("10Gi"),
	})
	if err != nil {
		t.Fatalf("CreateDisk() error = %v", err)
	}

	if disk.Name != "extra-disk" {
		t.Errorf("disk.Name = %q, want %q", disk.Name, "extra-disk")
	}
	if disk.Phase != string(v1alpha2.DiskReady) {
		t.Errorf("disk.Phase = %q, want %q", disk.Phase, v1alpha2.DiskReady)
	}
	if disk.StorageClass != "default-sc" {
		t.Errorf("disk.StorageClass = %q, want provider default %q", disk.StorageClass, "default-sc")
	}
	if want := resource.MustParse("10Gi"); disk.Size.Cmp(want) != 0 {
		t.Errorf("disk.Size = %s, want %s", disk.Size.String(), want.String())
	}

	stored := fake.disks[fvKey(testDisksNamespace, "extra-disk")]
	if stored == nil {
		t.Fatal("VirtualDisk was not stored on the base cluster")
	}
	if stored.Labels[vm.ManagedByLabelKey] != vm.ManagedByLabelValue {
		t.Errorf("VirtualDisk labels = %v, want managed-by label", stored.Labels)
	}
	if stored.Spec.DataSource != nil {
		t.Error("blank disk must not carry a DataSource")
	}
}

func TestCreateDiskUsesSpecStorageClass(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskReady
	}
	m := newTestDiskManager(fake)

	disk, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{
		Name:         "extra-disk",
		Size:         resource.MustParse("1Gi"),
		StorageClass: "custom-sc",
	})
	if err != nil {
		t.Fatalf("CreateDisk() error = %v", err)
	}
	if disk.StorageClass != "custom-sc" {
		t.Errorf("disk.StorageClass = %q, want %q", disk.StorageClass, "custom-sc")
	}
}

func TestCreateDiskTreatsWaitForFirstConsumerAsReady(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskWaitForFirstConsumer
	}
	m := newTestDiskManager(fake)

	disk, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{
		Name: "wffc-disk",
		Size: resource.MustParse("1Gi"),
	})
	if err != nil {
		t.Fatalf("CreateDisk() error = %v", err)
	}
	if disk.Phase != string(v1alpha2.DiskWaitForFirstConsumer) {
		t.Errorf("disk.Phase = %q, want %q", disk.Phase, v1alpha2.DiskWaitForFirstConsumer)
	}
}

func TestCreateDiskFailsOnFailedPhase(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskFailed
	}
	m := newTestDiskManager(fake)

	_, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{
		Name: "bad-disk",
		Size: resource.MustParse("1Gi"),
	})
	if err == nil || !strings.Contains(err.Error(), "Failed") {
		t.Fatalf("CreateDisk() error = %v, want Failed-phase error", err)
	}
}

func TestCreateDiskValidatesSpec(t *testing.T) {
	t.Parallel()

	m := newTestDiskManager(newFakeVirt())

	if _, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{Size: resource.MustParse("1Gi")}); err == nil {
		t.Error("CreateDisk() with empty name: want error")
	}
	if _, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{Name: "d"}); err == nil {
		t.Error("CreateDisk() with zero size: want error")
	}
}

func TestCreateDiskTimesOutWhilePending(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskPending
	}
	m := newTestDiskManager(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := m.CreateDisk(ctx, clusterprovider.DiskSpec{
		Name: "pending-disk",
		Size: resource.MustParse("1Gi"),
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("CreateDisk() error = %v, want context deadline error", err)
	}
}

func TestDeleteDiskWaitsUntilGone(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateDisk = func(vd *v1alpha2.VirtualDisk) {
		vd.Status.Phase = v1alpha2.DiskReady
	}
	m := newTestDiskManager(fake)

	if _, err := m.CreateDisk(context.Background(), clusterprovider.DiskSpec{
		Name: "doomed-disk",
		Size: resource.MustParse("1Gi"),
	}); err != nil {
		t.Fatalf("CreateDisk() error = %v", err)
	}

	if err := m.DeleteDisk(context.Background(), "doomed-disk"); err != nil {
		t.Fatalf("DeleteDisk() error = %v", err)
	}
	if _, ok := fake.disks[fvKey(testDisksNamespace, "doomed-disk")]; ok {
		t.Error("VirtualDisk still present after DeleteDisk")
	}
}

func TestDeleteDiskIsIdempotent(t *testing.T) {
	t.Parallel()

	m := newTestDiskManager(newFakeVirt())
	if err := m.DeleteDisk(context.Background(), "never-existed"); err != nil {
		t.Fatalf("DeleteDisk() on absent disk error = %v, want nil", err)
	}
}

func TestAttachDiskCreatesAttachmentAndWaits(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateVMBDA = func(vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) {
		vmbda.Status.Phase = v1alpha2.BlockDeviceAttachmentPhaseAttached
	}
	m := newTestDiskManager(fake)

	if err := m.AttachDisk(context.Background(), "worker-0", "extra-disk"); err != nil {
		t.Fatalf("AttachDisk() error = %v", err)
	}

	stored := fake.vmbdas[fvKey(testDisksNamespace, "extra-disk-worker-0")]
	if stored == nil {
		t.Fatal("VMBDA was not created")
	}
	if stored.Spec.VirtualMachineName != "worker-0" {
		t.Errorf("VMBDA VM name = %q, want %q", stored.Spec.VirtualMachineName, "worker-0")
	}
	if stored.Spec.BlockDeviceRef.Kind != v1alpha2.VMBDAObjectRefKindVirtualDisk || stored.Spec.BlockDeviceRef.Name != "extra-disk" {
		t.Errorf("VMBDA block device ref = %+v, want VirtualDisk/extra-disk", stored.Spec.BlockDeviceRef)
	}
	if stored.Labels[vm.ManagedByLabelKey] != vm.ManagedByLabelValue {
		t.Errorf("VMBDA labels = %v, want managed-by label", stored.Labels)
	}
}

func TestAttachDiskFailsOnFailedPhase(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateVMBDA = func(vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) {
		vmbda.Status.Phase = v1alpha2.BlockDeviceAttachmentPhaseFailed
	}
	m := newTestDiskManager(fake)

	err := m.AttachDisk(context.Background(), "worker-0", "extra-disk")
	if err == nil || !strings.Contains(err.Error(), "Failed") {
		t.Fatalf("AttachDisk() error = %v, want Failed-phase error", err)
	}
}

func TestAttachDiskConvergesOnExistingAttachment(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateVMBDA = func(vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) {
		vmbda.Status.Phase = v1alpha2.BlockDeviceAttachmentPhaseAttached
	}
	m := newTestDiskManager(fake)

	if err := m.AttachDisk(context.Background(), "worker-0", "extra-disk"); err != nil {
		t.Fatalf("first AttachDisk() error = %v", err)
	}
	// The retry hits AlreadyExists and must still succeed by waiting on the
	// existing attachment.
	if err := m.AttachDisk(context.Background(), "worker-0", "extra-disk"); err != nil {
		t.Fatalf("second AttachDisk() error = %v, want nil", err)
	}
}

func TestDetachDiskWaitsUntilGone(t *testing.T) {
	t.Parallel()

	fake := newFakeVirt()
	fake.onCreateVMBDA = func(vmbda *v1alpha2.VirtualMachineBlockDeviceAttachment) {
		vmbda.Status.Phase = v1alpha2.BlockDeviceAttachmentPhaseAttached
	}
	m := newTestDiskManager(fake)

	if err := m.AttachDisk(context.Background(), "worker-0", "extra-disk"); err != nil {
		t.Fatalf("AttachDisk() error = %v", err)
	}
	if err := m.DetachDisk(context.Background(), "worker-0", "extra-disk"); err != nil {
		t.Fatalf("DetachDisk() error = %v", err)
	}
	if _, ok := fake.vmbdas[fvKey(testDisksNamespace, "extra-disk-worker-0")]; ok {
		t.Error("VMBDA still present after DetachDisk")
	}
}

func TestDetachDiskIsIdempotent(t *testing.T) {
	t.Parallel()

	m := newTestDiskManager(newFakeVirt())
	if err := m.DetachDisk(context.Background(), "worker-0", "never-attached"); err != nil {
		t.Fatalf("DetachDisk() on absent attachment error = %v, want nil", err)
	}
}

func TestAttachmentName(t *testing.T) {
	t.Parallel()

	if got := attachmentName("disk", "node"); got != "disk-node" {
		t.Errorf("attachmentName(short) = %q, want %q", got, "disk-node")
	}

	longDisk := strings.Repeat("d", 40)
	longNode := strings.Repeat("n", 40)
	got := attachmentName(longDisk, longNode)
	if len(got) > 63 {
		t.Errorf("attachmentName(long) length = %d, want <= 63 (%q)", len(got), got)
	}
	if again := attachmentName(longDisk, longNode); again != got {
		t.Errorf("attachmentName is not deterministic: %q != %q", again, got)
	}
	if other := attachmentName(longDisk, strings.Repeat("m", 40)); other == got {
		t.Errorf("attachmentName collision for different nodes: %q", other)
	}
}
