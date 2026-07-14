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

package clusterprovider

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/api/resource"
)

// ErrDisksUnsupported is returned by DiskManager operations when the cluster
// provider does not support disk management (yet).
var ErrDisksUnsupported = errors.New("cluster provider does not support disk management")

// DiskSpec describes the additional disk to create. Name and Size are
// required; an empty StorageClass means the provider's default.
type DiskSpec struct {
	Name         string
	Size         resource.Quantity
	StorageClass string
}

// Disk describes a provider-managed additional disk. Phase is
// provider-specific (e.g. "Ready" for DVP).
type Disk struct {
	Name         string
	Size         resource.Quantity
	StorageClass string
	Phase        string
}

// DiskManager manages additional block devices on the test cluster's nodes.
// How a disk materializes is provider-specific: DVP creates VirtualDisk /
// VirtualMachineBlockDeviceAttachment resources on the base cluster; Commander
// would converge the cluster from an adjusted template (not implemented yet).
//
// All operations block until the target state is reached (disk ready,
// attachment attached, resource gone); bound the wait via ctx.
type DiskManager interface {
	CreateDisk(ctx context.Context, spec DiskSpec) (*Disk, error)
	// DeleteDisk removes the disk; it must be detached from all nodes first.
	DeleteDisk(ctx context.Context, diskName string) error
	AttachDisk(ctx context.Context, nodeName, diskName string) error
	// DetachDisk removes the attachment; the disk itself is kept.
	DetachDisk(ctx context.Context, nodeName, diskName string) error
}
