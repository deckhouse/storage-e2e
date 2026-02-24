/*
Copyright 2025 Flant JSC

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

package kubernetes

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
)

// VirtualDiskAttachmentConfig holds configuration for attaching a virtual disk to a VM
type VirtualDiskAttachmentConfig struct {
	// VMName is the name of the VirtualMachine to attach the disk to
	VMName string
	// Namespace is the namespace where the VM and disk resources are located
	Namespace string
	// DiskName is the name for the new VirtualDisk (optional, auto-generated if empty)
	DiskName string
	// DiskSize is the size of the disk (e.g., "200Gi")
	DiskSize string
	// StorageClassName is the storage class to use for the disk
	StorageClassName string
}

// VirtualDiskAttachmentResult holds the result of attaching a virtual disk
type VirtualDiskAttachmentResult struct {
	// DiskName is the name of the created VirtualDisk
	DiskName string
	// AttachmentName is the name of the created VirtualMachineBlockDeviceAttachment
	AttachmentName string
}

// AttachVirtualDiskToVM creates a VirtualDisk and attaches it to the specified VM using VirtualMachineBlockDeviceAttachment.
// The disk is created as a blank disk with the specified size and storage class.
// Returns the names of created resources for later use (e.g., waiting for attachment or cleanup).
func AttachVirtualDiskToVM(ctx context.Context, kubeconfig *rest.Config, config VirtualDiskAttachmentConfig) (*VirtualDiskAttachmentResult, error) {
	if config.VMName == "" {
		return nil, fmt.Errorf("VMName is required")
	}
	if config.Namespace == "" {
		return nil, fmt.Errorf("Namespace is required")
	}
	if config.DiskSize == "" {
		return nil, fmt.Errorf("DiskSize is required")
	}
	if config.StorageClassName == "" {
		return nil, fmt.Errorf("StorageClassName is required")
	}

	// Generate disk name if not provided
	diskName := config.DiskName
	if diskName == "" {
		diskName = fmt.Sprintf("%s-data-disk", config.VMName)
	}
	attachmentName := fmt.Sprintf("%s-attachment", diskName)

	logger.Info("Attaching VirtualDisk %s (%s) to VM %s in namespace %s", diskName, config.DiskSize, config.VMName, config.Namespace)

	// Create virtualization client
	virtClient, err := virtualization.NewClient(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create virtualization client: %w", err)
	}

	// Parse disk size
	diskSize, err := resource.ParseQuantity(config.DiskSize)
	if err != nil {
		return nil, fmt.Errorf("failed to parse disk size %q: %w", config.DiskSize, err)
	}

	// Create VirtualDisk
	virtualDisk := &v1alpha2.VirtualDisk{
		ObjectMeta: metav1.ObjectMeta{
			Name:      diskName,
			Namespace: config.Namespace,
		},
		Spec: v1alpha2.VirtualDiskSpec{
			PersistentVolumeClaim: v1alpha2.VirtualDiskPersistentVolumeClaim{
				Size:         &diskSize,
				StorageClass: &config.StorageClassName,
			},
		},
	}

	err = virtClient.VirtualDisks().Create(ctx, virtualDisk)
	if err != nil {
		return nil, fmt.Errorf("failed to create VirtualDisk %s: %w", diskName, err)
	}
	logger.Success("VirtualDisk %s created", diskName)

	// Create VirtualMachineBlockDeviceAttachment
	attachment := &v1alpha2.VirtualMachineBlockDeviceAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      attachmentName,
			Namespace: config.Namespace,
		},
		Spec: v1alpha2.VirtualMachineBlockDeviceAttachmentSpec{
			VirtualMachineName: config.VMName,
			BlockDeviceRef: v1alpha2.VMBDAObjectRef{
				Kind: v1alpha2.VMBDAObjectRefKindVirtualDisk,
				Name: diskName,
			},
		},
	}

	err = virtClient.VirtualMachineBlockDeviceAttachments().Create(ctx, attachment)
	if err != nil {
		return nil, fmt.Errorf("failed to create VirtualMachineBlockDeviceAttachment %s: %w", attachmentName, err)
	}
	logger.Success("VirtualMachineBlockDeviceAttachment %s created", attachmentName)

	return &VirtualDiskAttachmentResult{
		DiskName:       diskName,
		AttachmentName: attachmentName,
	}, nil
}

// WaitForVirtualDiskAttached waits for the VirtualMachineBlockDeviceAttachment to reach the Attached phase.
// It polls the attachment status until it's attached or the context is cancelled/times out.
// The pollInterval parameter specifies how often to check the status (recommended: 10 seconds).
func WaitForVirtualDiskAttached(ctx context.Context, kubeconfig *rest.Config, namespace, attachmentName string, pollInterval time.Duration) error {
	logger.Info("Waiting for VirtualMachineBlockDeviceAttachment %s/%s to be attached...", namespace, attachmentName)

	// Create virtualization client
	virtClient, err := virtualization.NewClient(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create virtualization client: %w", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for disk attachment: %w", ctx.Err())
		case <-ticker.C:
			attachment, err := virtClient.VirtualMachineBlockDeviceAttachments().Get(ctx, namespace, attachmentName)
			if err != nil {
				logger.Warn("Error getting VirtualMachineBlockDeviceAttachment %s/%s: %v. Retrying...", namespace, attachmentName, err)
				continue
			}

			phase := attachment.Status.Phase
			logger.Debug("VirtualMachineBlockDeviceAttachment %s/%s phase: %s", namespace, attachmentName, phase)

			if phase == v1alpha2.BlockDeviceAttachmentPhaseAttached {
				logger.Success("VirtualDisk successfully attached (attachment: %s/%s)", namespace, attachmentName)
				return nil
			}

			// Check for failure phases
			if phase == v1alpha2.BlockDeviceAttachmentPhaseFailed {
				return fmt.Errorf("disk attachment failed: attachment %s/%s is in Failed phase", namespace, attachmentName)
			}
		}
	}
}
