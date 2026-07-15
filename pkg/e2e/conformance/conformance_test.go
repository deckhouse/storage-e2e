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

package conformance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deckhouse/storage-e2e/pkg/e2e"
)

// execFunc adapts a bare function to the NodeExecutor contract.
type execFunc func(ctx context.Context, nodeName, command string) (e2e.ExecResult, error)

func (f execFunc) Exec(ctx context.Context, nodeName, command string) (e2e.ExecResult, error) {
	return f(ctx, nodeName, command)
}

// fakeNode serves a mutable block-device list in `lsblk -dno NAME,TYPE` form.
type fakeNode struct {
	devices []string
}

func (n *fakeNode) Exec(context.Context, string, string) (e2e.ExecResult, error) {
	var b strings.Builder
	for _, d := range n.devices {
		fmt.Fprintf(&b, "%s disk\n", d)
	}
	return e2e.ExecResult{Stdout: []byte(b.String())}, nil
}

// fakeDisks implements DiskManager in memory; AttachDisk/DetachDisk mutate the
// bound fakeNode's device list the way a real hotplug would.
type fakeDisks struct {
	node *fakeNode

	created  []string
	deleted  []string
	attached []string
	detached []string

	// Overrides (nil = default success behavior).
	createErr    error
	createdName  string // CreateDisk reports this name instead of spec.Name
	attachNoShow bool   // AttachDisk succeeds but no device appears on the node
}

func deviceFor(diskName string) string { return "dev-" + diskName }

func (d *fakeDisks) CreateDisk(_ context.Context, spec e2e.DiskSpec) (*e2e.Disk, error) {
	if d.createErr != nil {
		return nil, d.createErr
	}
	d.created = append(d.created, spec.Name)
	name := spec.Name
	if d.createdName != "" {
		name = d.createdName
	}
	return &e2e.Disk{Name: name, Size: spec.Size}, nil
}

func (d *fakeDisks) DeleteDisk(_ context.Context, diskName string) error {
	d.deleted = append(d.deleted, diskName)
	return nil
}

func (d *fakeDisks) AttachDisk(_ context.Context, _, diskName string) error {
	d.attached = append(d.attached, diskName)
	if !d.attachNoShow {
		d.node.devices = append(d.node.devices, deviceFor(diskName))
	}
	return nil
}

func (d *fakeDisks) DetachDisk(_ context.Context, _, diskName string) error {
	d.detached = append(d.detached, diskName)
	d.node.devices = slices.DeleteFunc(d.node.devices, func(dev string) bool {
		return dev == deviceFor(diskName)
	})
	return nil
}

func TestVerifyDiskLifecycleHappyPath(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda", "vdb"}}
	disks := &fakeDisks{node: node}

	if err := verifyDiskLifecycle(context.Background(), disks, node, "worker-0"); err != nil {
		t.Fatalf("verifyDiskLifecycle() error = %v", err)
	}

	if len(disks.created) != 1 {
		t.Fatalf("created disks = %v, want exactly one", disks.created)
	}
	diskName := disks.created[0]
	if !slices.Contains(disks.attached, diskName) {
		t.Errorf("disk %q was never attached", diskName)
	}
	if !slices.Contains(disks.detached, diskName) {
		t.Errorf("disk %q was never detached", diskName)
	}
	if !slices.Contains(disks.deleted, diskName) {
		t.Errorf("disk %q was never deleted", diskName)
	}
	if got, want := node.devices, []string{"vda", "vdb"}; !slices.Equal(got, want) {
		t.Errorf("node devices after lifecycle = %v, want %v", got, want)
	}
}

func TestVerifyDiskLifecycleFailsWhenDeviceNeverAppears(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda"}}
	disks := &fakeDisks{node: node, attachNoShow: true}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := verifyDiskLifecycle(ctx, disks, node, "worker-0")
	if err == nil || !strings.Contains(err.Error(), "after attach") {
		t.Fatalf("verifyDiskLifecycle() error = %v, want after-attach timeout", err)
	}

	// Best-effort teardown must still run on failure.
	diskName := disks.created[0]
	if !slices.Contains(disks.detached, diskName) || !slices.Contains(disks.deleted, diskName) {
		t.Errorf("teardown incomplete: detached=%v deleted=%v, want both to contain %q",
			disks.detached, disks.deleted, diskName)
	}
}

func TestVerifyDiskLifecycleFailsOnCreatedNameMismatch(t *testing.T) {
	t.Parallel()

	node := &fakeNode{}
	disks := &fakeDisks{node: node, createdName: "something-else"}

	err := verifyDiskLifecycle(context.Background(), disks, node, "worker-0")
	if err == nil || !strings.Contains(err.Error(), "name mismatch") {
		t.Fatalf("verifyDiskLifecycle() error = %v, want name-mismatch error", err)
	}
}

// unsupportedFake returns ErrDisksUnsupported (wrapped, like the e2e facade
// stub does) from every operation except those explicitly overridden.
type unsupportedFake struct {
	deleteErr error
	attachErr error
	detachErr error

	set bool // when true, the *Err fields are used as-is (nil included)
}

func unsupportedErr() error {
	return fmt.Errorf("provider %q: %w", "fake", e2e.ErrDisksUnsupported)
}

func (u unsupportedFake) CreateDisk(context.Context, e2e.DiskSpec) (*e2e.Disk, error) {
	return nil, unsupportedErr()
}

func (u unsupportedFake) DeleteDisk(context.Context, string) error {
	if u.set {
		return u.deleteErr
	}
	return unsupportedErr()
}

func (u unsupportedFake) AttachDisk(context.Context, string, string) error {
	if u.set {
		return u.attachErr
	}
	return unsupportedErr()
}

func (u unsupportedFake) DetachDisk(context.Context, string, string) error {
	if u.set {
		return u.detachErr
	}
	return unsupportedErr()
}

func TestVerifyDiskLifecyclePassesOnConsistentlyUnsupportedProvider(t *testing.T) {
	t.Parallel()

	node := &fakeNode{}
	if err := verifyDiskLifecycle(context.Background(), unsupportedFake{}, node, "worker-0"); err != nil {
		t.Fatalf("verifyDiskLifecycle() on unsupported provider error = %v, want nil", err)
	}
}

func TestVerifyDisksUnsupportedStubRejectsInconsistentProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		disks    e2e.DiskManager
		wantPart string
	}{
		{
			name: "DeleteDisk silently succeeds",
			disks: unsupportedFake{
				set:       true,
				attachErr: unsupportedErr(),
				detachErr: unsupportedErr(),
			},
			wantPart: "DeleteDisk",
		},
		{
			name: "AttachDisk returns a different error",
			disks: unsupportedFake{
				set:       true,
				deleteErr: unsupportedErr(),
				attachErr: errors.New("boom"),
				detachErr: unsupportedErr(),
			},
			wantPart: "AttachDisk",
		},
		{
			name: "DetachDisk silently succeeds",
			disks: unsupportedFake{
				set:       true,
				deleteErr: unsupportedErr(),
				attachErr: unsupportedErr(),
			},
			wantPart: "DetachDisk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := verifyDisksUnsupportedStub(context.Background(), tt.disks)
			if err == nil || !strings.Contains(err.Error(), tt.wantPart) {
				t.Fatalf("verifyDisksUnsupportedStub() error = %v, want mention of %s", err, tt.wantPart)
			}
		})
	}
}

func TestBlockDeviceNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		result  e2e.ExecResult
		execErr error
		want    []string
		wantErr bool
	}{
		{
			name:   "disks only, partitions and rom skipped",
			result: e2e.ExecResult{Stdout: []byte("vda disk\nvda1 part\nsr0 rom\nvdb disk\n")},
			want:   []string{"vda", "vdb"},
		},
		{
			name:   "garbage lines ignored",
			result: e2e.ExecResult{Stdout: []byte("\nnot-enough\none two three\nvda disk\n")},
			want:   []string{"vda"},
		},
		{
			name:   "empty output",
			result: e2e.ExecResult{Stdout: nil},
			want:   nil,
		},
		{
			name:    "non-zero exit code",
			result:  e2e.ExecResult{ExitCode: 127, Stderr: []byte("lsblk: not found")},
			wantErr: true,
		},
		{
			name:    "transport error",
			execErr: errors.New("ssh: connection lost"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := execFunc(func(context.Context, string, string) (e2e.ExecResult, error) {
				return tt.result, tt.execErr
			})
			got, err := blockDeviceNames(context.Background(), nodes, "worker-0")
			if (err != nil) != tt.wantErr {
				t.Fatalf("blockDeviceNames() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("blockDeviceNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWaitNewBlockDeviceReturnsTheAddedDevice(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda", "vdc"}}
	device, err := waitNewBlockDevice(context.Background(), node, "worker-0", []string{"vda"})
	if err != nil {
		t.Fatalf("waitNewBlockDevice() error = %v, want nil", err)
	}
	if device != "vdc" {
		t.Errorf("waitNewBlockDevice() = %q, want %q", device, "vdc")
	}
}

func TestWaitNewBlockDeviceTimesOutWhenNothingAppears(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := waitNewBlockDevice(ctx, node, "worker-0", []string{"vda"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitNewBlockDevice() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestWaitNewBlockDeviceFailsOnAmbiguousExtraDevices(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda", "vdb", "vdc"}}
	_, err := waitNewBlockDevice(context.Background(), node, "worker-0", []string{"vda"})
	if err == nil || !strings.Contains(err.Error(), "cannot attribute") {
		t.Fatalf("waitNewBlockDevice() error = %v, want unattributable-devices error", err)
	}
}

func TestWaitNewBlockDeviceReportsLastExecError(t *testing.T) {
	t.Parallel()

	nodes := execFunc(func(context.Context, string, string) (e2e.ExecResult, error) {
		return e2e.ExecResult{}, errors.New("ssh: connection lost")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := waitNewBlockDevice(ctx, nodes, "worker-0", nil)
	if err == nil || !strings.Contains(err.Error(), "ssh: connection lost") {
		t.Fatalf("waitNewBlockDevice() error = %v, want the last exec error preserved", err)
	}
}

func TestWaitBlockDeviceGone(t *testing.T) {
	t.Parallel()

	node := &fakeNode{devices: []string{"vda"}}
	if err := waitBlockDeviceGone(context.Background(), node, "worker-0", "vdb"); err != nil {
		t.Fatalf("waitBlockDeviceGone(absent device) error = %v, want nil", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := waitBlockDeviceGone(ctx, node, "worker-0", "vda")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitBlockDeviceGone(present device) error = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), `"vda"`) {
		t.Errorf("waitBlockDeviceGone() error = %v, want mention of the device", err)
	}
}
