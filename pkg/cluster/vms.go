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

package cluster

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VMResources tracks VM-related resources created for a test cluster
type VMResources struct {
	VirtClient *virtualization.Client
	Namespace  string
	VMNames    []string
	CVMINames  []string
}

// CreateVirtualMachines creates virtual machines from cluster definition.
// It validates CLUSTER_CREATE_MODE, handles VM name conflicts, creates all VMs,
// and returns the list of VM names that were created along with resource tracking info.
func CreateVirtualMachines(ctx context.Context, virtClient *virtualization.Client, clusterDef *config.ClusterDefinition) ([]string, *VMResources, error) {
	// Check CLUSTER_CREATE_MODE
	if config.TestClusterCreateMode != config.ClusterCreateModeAlwaysCreateNew {
		return nil, nil, fmt.Errorf("CLUSTER_CREATE_MODE must be set to '%s'. Current value: '%s'. Using existing cluster currently is not supported", config.ClusterCreateModeAlwaysCreateNew, config.TestClusterCreateMode)
	}

	namespace := config.TestClusterNamespace

	// Get all VM nodes from cluster definition
	vmNodes := getVMNodes(clusterDef)
	if len(vmNodes) == 0 {
		return nil, nil, fmt.Errorf("no VM nodes found in cluster definition")
	}

	// Track CVMI names that we create or use
	cvmiNamesMap := make(map[string]bool)

	vmNames := make([]string, 0, len(vmNodes))
	for _, node := range vmNodes {
		vmNames = append(vmNames, node.Hostname)
	}

	// Check for conflicts in all resources before creating anything
	conflicts, err := checkResourceConflicts(ctx, virtClient, namespace, vmNodes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check for resource conflicts: %w", err)
	}

	// If any conflicts exist, fail with a detailed error message
	if len(conflicts.VMs) > 0 || len(conflicts.VirtualDisks) > 0 || len(conflicts.ClusterVirtualImages) > 0 {
		conflictMessages := make([]string, 0)
		if len(conflicts.VMs) > 0 {
			conflictMessages = append(conflictMessages, fmt.Sprintf("VirtualMachines: %v", conflicts.VMs))
		}
		if len(conflicts.VirtualDisks) > 0 {
			conflictMessages = append(conflictMessages, fmt.Sprintf("VirtualDisks: %v", conflicts.VirtualDisks))
		}
		if len(conflicts.ClusterVirtualImages) > 0 {
			conflictMessages = append(conflictMessages, fmt.Sprintf("ClusterVirtualImages: %v", conflicts.ClusterVirtualImages))
		}
		return nil, nil, fmt.Errorf("the following VM-related resources already exist (CLUSTER_CREATE_MODE=%s): %s", config.TestClusterCreateMode, strings.Join(conflictMessages, ", "))
	}

	// Create all VMs
	storageClass := config.TestClusterStorageClass
	for _, node := range vmNodes {
		cvmiName, err := createVM(ctx, virtClient, namespace, node, storageClass)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create VM %s: %w", node.Hostname, err)
		}
		if cvmiName != "" {
			cvmiNamesMap[cvmiName] = true
		}
	}

	// Convert CVMI names map to slice
	cvmiNames := make([]string, 0, len(cvmiNamesMap))
	for name := range cvmiNamesMap {
		cvmiNames = append(cvmiNames, name)
	}

	resources := &VMResources{
		VirtClient: virtClient,
		Namespace:  namespace,
		VMNames:    vmNames,
		CVMINames:  cvmiNames,
	}

	return vmNames, resources, nil
}

// resourceConflicts tracks conflicts in different resource types
type resourceConflicts struct {
	VMs                  []string
	VirtualDisks         []string
	ClusterVirtualImages []string
}

// checkResourceConflicts checks for conflicts in all VM-related resources
func checkResourceConflicts(ctx context.Context, virtClient *virtualization.Client, namespace string, vmNodes []config.ClusterNode) (*resourceConflicts, error) {
	conflicts := &resourceConflicts{
		VMs:                  make([]string, 0),
		VirtualDisks:         make([]string, 0),
		ClusterVirtualImages: make([]string, 0),
	}

	// Collect all resource names we plan to create
	vmNames := make([]string, 0, len(vmNodes))
	systemDiskNames := make([]string, 0, len(vmNodes))
	cvmiNamesSet := make(map[string]bool)

	for _, node := range vmNodes {
		vmName := node.Hostname
		vmNames = append(vmNames, vmName)
		systemDiskName := fmt.Sprintf("%s-system", vmName)
		systemDiskNames = append(systemDiskNames, systemDiskName)

		// Get CVMI name from image URL
		cvmiName := getCVMINameFromImageURL(node.OSType.ImageURL)
		cvmiNamesSet[cvmiName] = true
	}

	// Check for conflicting VirtualMachines
	existingVMs, err := virtClient.VirtualMachines().List(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing VMs: %w", err)
	}
	existingVMNames := make(map[string]bool)
	for _, vm := range existingVMs {
		existingVMNames[vm.Name] = true
	}
	for _, vmName := range vmNames {
		if existingVMNames[vmName] {
			conflicts.VMs = append(conflicts.VMs, vmName)
		}
	}

	// Check for conflicting VirtualDisks
	existingVDs, err := virtClient.VirtualDisks().List(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing VirtualDisks: %w", err)
	}
	existingVDNames := make(map[string]bool)
	for _, vd := range existingVDs {
		existingVDNames[vd.Name] = true
	}
	for _, diskName := range systemDiskNames {
		if existingVDNames[diskName] {
			conflicts.VirtualDisks = append(conflicts.VirtualDisks, diskName)
		}
	}

	// Check for conflicting ClusterVirtualImages (cluster-scoped, no namespace)
	cvmiNames := make([]string, 0, len(cvmiNamesSet))
	for name := range cvmiNamesSet {
		cvmiNames = append(cvmiNames, name)
	}
	for _, cvmiName := range cvmiNames {
		_, err := virtClient.ClusterVirtualImages().Get(ctx, cvmiName)
		if err == nil {
			// CVMI exists
			conflicts.ClusterVirtualImages = append(conflicts.ClusterVirtualImages, cvmiName)
		} else if !errors.IsNotFound(err) {
			// Some other error occurred
			return nil, fmt.Errorf("failed to check ClusterVirtualImage %s: %w", cvmiName, err)
		}
		// If IsNotFound, the CVMI doesn't exist, which is fine
	}

	return conflicts, nil
}

// getVMNodes extracts all VM nodes from cluster definition
func getVMNodes(clusterDef *config.ClusterDefinition) []config.ClusterNode {
	var vmNodes []config.ClusterNode

	for _, node := range clusterDef.Masters {
		if node.HostType == config.HostTypeVM {
			vmNodes = append(vmNodes, node)
		}
	}

	for _, node := range clusterDef.Workers {
		if node.HostType == config.HostTypeVM {
			vmNodes = append(vmNodes, node)
		}
	}

	if clusterDef.Setup != nil && clusterDef.Setup.HostType == config.HostTypeVM {
		vmNodes = append(vmNodes, *clusterDef.Setup)
	}

	return vmNodes
}

// createVM creates a virtual machine with all required dependencies
// Returns the CVMI name that was used/created
func createVM(ctx context.Context, virtClient *virtualization.Client, namespace string, node config.ClusterNode, storageClass string) (string, error) {
	vmName := node.Hostname

	// 1. Create or get ClusterVirtualImage
	cvmiName := getCVMINameFromImageURL(node.OSType.ImageURL)
	cvmi, err := virtClient.ClusterVirtualImages().Get(ctx, cvmiName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get ClusterVirtualImage %s: %w", cvmiName, err)
		}
		// CVMI doesn't exist, create it
		cvmi = &v1alpha2.ClusterVirtualImage{
			ObjectMeta: metav1.ObjectMeta{
				Name: cvmiName,
			},
			Spec: v1alpha2.ClusterVirtualImageSpec{
				DataSource: v1alpha2.ClusterVirtualImageDataSource{
					Type: "HTTP",
					HTTP: &v1alpha2.DataSourceHTTP{
						URL: node.OSType.ImageURL,
					},
				},
			},
		}
		err = virtClient.ClusterVirtualImages().Create(ctx, cvmi)
		if err != nil {
			return "", fmt.Errorf("failed to create ClusterVirtualImage %s: %w", cvmiName, err)
		}
	}

	// 2. Create system VirtualDisk (check if it exists first)
	systemDiskName := fmt.Sprintf("%s-system", vmName)
	_, err = virtClient.VirtualDisks().Get(ctx, namespace, systemDiskName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check VirtualDisk %s: %w", systemDiskName, err)
		}
		// VirtualDisk doesn't exist, create it
		systemDisk := &v1alpha2.VirtualDisk{
			ObjectMeta: metav1.ObjectMeta{
				Name:      systemDiskName,
				Namespace: namespace,
			},
			Spec: v1alpha2.VirtualDiskSpec{
				PersistentVolumeClaim: v1alpha2.VirtualDiskPersistentVolumeClaim{
					Size:         resource.NewQuantity(int64(node.DiskSize)*1024*1024*1024, resource.BinarySI),
					StorageClass: &storageClass,
				},
				DataSource: &v1alpha2.VirtualDiskDataSource{
					Type: "ObjectRef",
					ObjectRef: &v1alpha2.VirtualDiskObjectRef{
						Kind: "ClusterVirtualImage",
						Name: cvmi.Name,
					},
				},
			},
		}
		err = virtClient.VirtualDisks().Create(ctx, systemDisk)
		if err != nil {
			return "", fmt.Errorf("failed to create system VirtualDisk %s: %w", systemDiskName, err)
		}
	}
	// If VirtualDisk already exists, we'll use it

	// 3. Create VirtualMachine (check if it exists first)
	_, err = virtClient.VirtualMachines().Get(ctx, namespace, vmName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", fmt.Errorf("failed to check VirtualMachine %s: %w", vmName, err)
		}
		// VirtualMachine doesn't exist, create it
		memoryQuantity := resource.MustParse(fmt.Sprintf("%dGi", node.RAM))
		vm := &v1alpha2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vmName,
				Namespace: namespace,
				Labels:    map[string]string{"vm": "linux", "service": "v1"},
			},
			Spec: v1alpha2.VirtualMachineSpec{
				VirtualMachineClassName:  "generic",
				EnableParavirtualization: true,
				RunPolicy:                v1alpha2.RunPolicy("AlwaysOn"),
				OsType:                   v1alpha2.OsType("Generic"),
				Bootloader:               v1alpha2.BootloaderType("BIOS"),
				LiveMigrationPolicy:      v1alpha2.LiveMigrationPolicy("PreferSafe"),
				CPU: v1alpha2.CPUSpec{
					Cores:        node.CPU,
					CoreFraction: "100%",
				},
				Memory: v1alpha2.MemorySpec{
					Size: memoryQuantity,
				},
				BlockDeviceRefs: []v1alpha2.BlockDeviceSpecRef{
					{
						Kind: v1alpha2.DiskDevice,
						Name: systemDiskName,
					},
				},
				Provisioning: &v1alpha2.Provisioning{
					Type:     "UserData",
					UserData: generateCloudInitUserData(vmName, config.VMSSHPublicKey),
				},
			},
		}
		err = virtClient.VirtualMachines().Create(ctx, vm)
		if err != nil {
			return "", fmt.Errorf("failed to create VirtualMachine %s: %w", vmName, err)
		}
	}
	// If VirtualMachine already exists, we'll skip creation

	return cvmiName, nil
}

// getCVMINameFromImageURL extracts a CVMI name from an image URL
func getCVMINameFromImageURL(imageURL string) string {
	// Extract filename from URL and use it as base name
	parts := strings.Split(imageURL, "/")
	filename := parts[len(parts)-1]
	// Remove extension
	name := strings.TrimSuffix(filename, ".img")
	name = strings.TrimSuffix(name, ".qcow2")
	// Make it Kubernetes-friendly (lowercase, replace dots with hyphens)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, ".", "-")
	return name
}

// generateCloudInitUserData generates cloud-init user data for VM provisioning
func generateCloudInitUserData(hostname, sshPubKey string) string {
	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - tmux
  - htop
  - qemu-guest-agent
  - iputils-ping
  - stress-ng
  - jq
  - yq
  - rsync
  - fio
  - curl

ssh_pwauth: true
users:
  - name: cloud
    # passwd: cloud
    passwd: $6$rounds=4096$vln/.aPHBOI7BMYR$bBMkqQvuGs5Gyd/1H5DP4m9HjQSy.kgrxpaGEHwkX7KEFV8BS.HZWPitAtZ2Vd8ZqIZRqmlykRCagTgPejt1i.
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    chpasswd: {expire: False}
    lock_passwd: false
    ssh_authorized_keys:
      - %s
write_files:
  - path: /etc/ssh/sshd_config.d/allow_tcp_forwarding.conf
    content: |
      # Разрешить TCP forwarding
      AllowTcpForwarding yes

runcmd:
  - systemctl restart ssh
  - hostnamectl set-hostname %s
  - systemctl daemon-reload
  - systemctl enable --now qemu-guest-agent.service
`, sshPubKey, hostname)
}

// CleanupVMResources forcefully stops and deletes virtual machines, virtual disks, and cluster virtual images.
// If a ClusterVirtualImage is in use by other resources, it will be skipped but VMs and VDs will still be deleted.
func CleanupVMResources(ctx context.Context, resources *VMResources) error {
	if resources == nil {
		return fmt.Errorf("resources cannot be nil")
	}

	// Step 1: Forcefully stop and delete Virtual Machines
	for _, vmName := range resources.VMNames {
		// Try to stop the VM by updating RunPolicy to Manual or by deleting directly
		// Deletion will stop the VM automatically
		err := resources.VirtClient.VirtualMachines().Delete(ctx, resources.Namespace, vmName)
		if err != nil && !errors.IsNotFound(err) {
			// Log but continue - we'll try to clean up other resources
			fmt.Printf("Warning: Failed to delete VM %s/%s: %v\n", resources.Namespace, vmName, err)
		}
	}

	// Step 2: Delete Virtual Disks
	// Delete system disks for our VMs
	for _, vmName := range resources.VMNames {
		systemDiskName := fmt.Sprintf("%s-system", vmName)
		err := resources.VirtClient.VirtualDisks().Delete(ctx, resources.Namespace, systemDiskName)
		if err != nil && !errors.IsNotFound(err) {
			fmt.Printf("Warning: Failed to delete VirtualDisk %s/%s: %v\n", resources.Namespace, systemDiskName, err)
		}
	}

	// Step 3: Check which ClusterVirtualImages are in use and delete those that aren't
	// Get all VirtualDisks across all namespaces to check for CVMI usage
	allVDisksAllNS, err := resources.VirtClient.VirtualDisks().List(ctx, "")
	if err != nil {
		fmt.Printf("Warning: Failed to list VirtualDisks across all namespaces: %v\n", err)
		allVDisksAllNS = []v1alpha2.VirtualDisk{}
	}

	// Build a map of CVMI names that are in use
	cvmiInUse := make(map[string]bool)
	for _, vd := range allVDisksAllNS {
		if vd.Spec.DataSource != nil && vd.Spec.DataSource.ObjectRef != nil {
			if vd.Spec.DataSource.ObjectRef.Kind == "ClusterVirtualImage" {
				cvmiInUse[vd.Spec.DataSource.ObjectRef.Name] = true
			}
		}
	}

	// Delete ClusterVirtualImages that are not in use
	for _, cvmiName := range resources.CVMINames {
		if cvmiInUse[cvmiName] {
			fmt.Printf("Skipping deletion of ClusterVirtualImage %s: still in use by other resources\n", cvmiName)
			continue
		}

		err := resources.VirtClient.ClusterVirtualImages().Delete(ctx, cvmiName)
		if err != nil && !errors.IsNotFound(err) {
			fmt.Printf("Warning: Failed to delete ClusterVirtualImage %s: %v\n", cvmiName, err)
		}
	}

	return nil
}
