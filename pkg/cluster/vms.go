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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"
)

const (
	vmClassAutoCreatedLabelKey   = "storage-e2e.deckhouse.io/auto-created"
	vmClassAutoCreatedLabelValue = "true"
)

// VMResources tracks VM-related resources created for a test cluster
type VMResources struct {
	VirtClient  *virtualization.Client
	Namespace   string
	VMNames     []string
	CVMINames   []string // ClusterVirtualImage names (cluster-scoped)
	SetupVMName string   // Name of the setup VM (always created)
}

// CreateVirtualMachines creates virtual machines from cluster definition.
// It handles VM name conflicts, creates all VMs in parallel,
// and returns the list of VM names that were created along with resource tracking info.
func CreateVirtualMachines(ctx context.Context, virtClient *virtualization.Client, clusterDef *config.ClusterDefinition) ([]string, *VMResources, error) {
	namespace := config.TestClusterNamespace

	// Get all VM nodes from cluster definition
	vmNodes := getVMNodes(clusterDef)
	if len(vmNodes) == 0 {
		return nil, nil, fmt.Errorf("no VM nodes found in cluster definition")
	}

	// Always add the default setup VM with a unique suffix
	setupVM := config.DefaultSetupVM
	// Generate unique suffix using timestamp
	suffix := fmt.Sprintf("%d", time.Now().Unix())
	setupVM.Hostname = setupVM.Hostname + suffix
	vmNodes = append(vmNodes, setupVM)
	setupVMName := setupVM.Hostname // Store the generated name for later use

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

	if err := ensureVirtualMachineClassForClusterVMs(ctx, virtClient); err != nil {
		return nil, nil, err
	}

	// Create all CVMIs first (with waiting for Ready)
	storageClass := config.TestClusterStorageClass
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(vmNodes))

	for _, node := range vmNodes {
		wg.Add(1)
		go func(n config.ClusterNode) {
			defer wg.Done()
			cvmiName := getCVMINameFromImageURL(n.OSType.ImageURL)
			if err := createCVI(ctx, virtClient, cvmiName, n.OSType.ImageURL); err != nil {
				errChan <- fmt.Errorf("failed to create/wait for CVI %s: %w", cvmiName, err)
				return
			}
			mu.Lock()
			cvmiNamesMap[cvmiName] = true
			mu.Unlock()
		}(node)
	}

	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return nil, nil, <-errChan
	}

	// Create all VMs in parallel
	var wg2 sync.WaitGroup
	errChan2 := make(chan error, len(vmNodes))

	for _, node := range vmNodes {
		wg2.Add(1)
		go func(n config.ClusterNode) {
			defer wg2.Done()
			if err := createVM(ctx, virtClient, namespace, n, storageClass); err != nil {
				errChan2 <- fmt.Errorf("failed to create VM %s: %w", n.Hostname, err)
			}
		}(node)
	}

	wg2.Wait()
	close(errChan2)

	if len(errChan2) > 0 {
		return nil, nil, <-errChan2
	}

	// Convert CVMI names map to slice
	cvmiNames := make([]string, 0, len(cvmiNamesMap))
	for name := range cvmiNamesMap {
		cvmiNames = append(cvmiNames, name)
	}

	// Track setup VM separately
	// The setup VM is always created, so it will exist in vmNames
	resources := &VMResources{
		VirtClient:  virtClient,
		Namespace:   namespace,
		VMNames:     vmNames,
		CVMINames:   cvmiNames,
		SetupVMName: setupVMName, // setupVMName was set above when creating setupVM
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
	// Only check if IMAGE_PULL_POLICY is "Always" - if "IfNotExists", we want to use existing CVIs
	if config.ImagePullPolicy == config.ImagePullPolicyAlways {
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
	}

	return conflicts, nil
}

// ensureVirtualMachineClassForClusterVMs ensures the configured VirtualMachineClass exists on the base cluster.
// When the name is not generic and the class is missing, it creates one by cloning spec from the built-in "generic"
// class and setting spec.cpu.type to Host. Inherited sizing policies and similar fields stay; spec.nodeSelector and
// spec.tolerations are cleared because Host CPU pins the instruction set to the node—keeping generic placement rules
// could allow heterogeneous nodes and break live migration (see Deckhouse VirtualMachineClass CPU type Host docs).
// The new object is labeled for identification and is never deleted by e2e cleanup.
func ensureVirtualMachineClassForClusterVMs(ctx context.Context, virtClient *virtualization.Client) error {
	className := config.EffectiveVirtualMachineClassName()
	if className == config.TestClusterVirtualMachineClassNameDefaultValue {
		return nil
	}

	vmcClient := virtClient.VirtualMachineClasses()

	_, err := vmcClient.Get(ctx, className)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("VirtualMachineClass %q: %w", className, err)
	}

	template, err := vmcClient.Get(ctx, config.TestClusterVirtualMachineClassNameDefaultValue)
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("VirtualMachineClass %q is missing and template class %q was not found on the cluster; cannot auto-create the class",
				className, config.TestClusterVirtualMachineClassNameDefaultValue)
		}
		return fmt.Errorf("get template VirtualMachineClass %q: %w", config.TestClusterVirtualMachineClassNameDefaultValue, err)
	}

	cloned := template.Spec.DeepCopy()
	cloned.CPU = v1alpha3.CPU{Type: v1alpha3.CPUTypeHost}
	cloned.NodeSelector = v1alpha3.NodeSelector{}
	cloned.Tolerations = nil

	vmc := &v1alpha3.VirtualMachineClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: className,
			Labels: map[string]string{
				vmClassAutoCreatedLabelKey: vmClassAutoCreatedLabelValue,
			},
		},
		Spec: *cloned,
	}

	if err := vmcClient.Create(ctx, vmc); err != nil {
		if errors.IsAlreadyExists(err) {
			return waitForVirtualMachineClassReady(ctx, virtClient, className)
		}
		return fmt.Errorf("create VirtualMachineClass %q: %w", className, err)
	}

	logger.Info("Created VirtualMachineClass %s (from generic template, cpu.type=Host, cleared nodeSelector/tolerations, label %s=%s)",
		className, vmClassAutoCreatedLabelKey, vmClassAutoCreatedLabelValue)
	return waitForVirtualMachineClassReady(ctx, virtClient, className)
}

func waitForVirtualMachineClassReady(ctx context.Context, virtClient *virtualization.Client, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, config.VirtualMachineClassReadinessTimeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastLog := time.Now()

	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timeout waiting for VirtualMachineClass %s to reach Ready (phase still not Ready after %v)",
				name, config.VirtualMachineClassReadinessTimeout)
		case <-ticker.C:
			vmc, err := virtClient.VirtualMachineClasses().Get(waitCtx, name)
			if err != nil {
				return fmt.Errorf("get VirtualMachineClass %s: %w", name, err)
			}
			switch vmc.Status.Phase {
			case v1alpha3.ClassPhaseReady:
				logger.Debug("VirtualMachineClass %s is Ready", name)
				return nil
			case v1alpha3.ClassPhaseTerminating:
				return fmt.Errorf("VirtualMachineClass %s is Terminating", name)
			default:
				if time.Since(lastLog) >= 30*time.Second {
					logger.Debug("VirtualMachineClass %s phase: %s", name, vmc.Status.Phase)
					lastLog = time.Now()
				}
			}
		}
	}
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

// randomizeHostnames appends a unique random suffix to each node hostname in the cluster definition
// to ensure unique iSCSI initiator names across cluster recreations.
// Each node gets its own suffix to minimize collision likelihood
// (e.g., "master-1" becomes "master-1-x7k2m", "worker-1" becomes "worker-1-r9g3p").
func randomizeHostnames(clusterDef *config.ClusterDefinition) {
	for i := range clusterDef.Masters {
		clusterDef.Masters[i].Hostname = clusterDef.Masters[i].Hostname + "-" + GenerateRandomSuffix(5)
	}
	for i := range clusterDef.Workers {
		clusterDef.Workers[i].Hostname = clusterDef.Workers[i].Hostname + "-" + GenerateRandomSuffix(5)
	}
}

// createCVI creates or gets a ClusterVirtualImage and waits for it to be Ready (15 min timeout)
func createCVI(ctx context.Context, virtClient *virtualization.Client, cvmiName, imageURL string) error {
	cviCtx, cancel := context.WithTimeout(ctx, config.ClusterVirtualImageReadinessTimeout)
	defer cancel()

	cvmi, getErr := virtClient.ClusterVirtualImages().Get(cviCtx, cvmiName)

	if getErr == nil && config.ImagePullPolicy == config.ImagePullPolicyAlways {
		return fmt.Errorf("ClusterVirtualImage %s already exists, cannot create a new one (IMAGE_PULL_POLICY=%s)", cvmiName, config.ImagePullPolicyAlways)
	}

	if errors.IsNotFound(getErr) {
		cvmi = &v1alpha2.ClusterVirtualImage{
			ObjectMeta: metav1.ObjectMeta{
				Name: cvmiName,
			},
			Spec: v1alpha2.ClusterVirtualImageSpec{
				DataSource: v1alpha2.ClusterVirtualImageDataSource{
					Type: "HTTP",
					HTTP: &v1alpha2.DataSourceHTTP{
						URL: imageURL,
					},
				},
			},
		}
		err := virtClient.ClusterVirtualImages().Create(cviCtx, cvmi)
		if err != nil && !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ClusterVirtualImage %s: %w", cvmiName, err)
		}
		if errors.IsAlreadyExists(err) {
			cvmi, err = virtClient.ClusterVirtualImages().Get(cviCtx, cvmiName)
			if err != nil {
				return fmt.Errorf("failed to get ClusterVirtualImage %s: %w", cvmiName, err)
			}
		}
	} else if getErr != nil {
		return fmt.Errorf("failed to get ClusterVirtualImage %s: %w", cvmiName, getErr)
	}

	return waitForClusterVirtualImageReady(cviCtx, virtClient, cvmiName)
}

// createVM creates a VirtualDisk and VirtualMachine for a node (20 sec timeout from parent context)
func createVM(ctx context.Context, virtClient *virtualization.Client, namespace string, node config.ClusterNode, storageClass string) error {
	vmName := node.Hostname
	cvmiName := getCVMINameFromImageURL(node.OSType.ImageURL)

	// Create system VirtualDisk
	systemDiskName := fmt.Sprintf("%s-system", vmName)
	_, vdErr := virtClient.VirtualDisks().Get(ctx, namespace, systemDiskName)
	if vdErr != nil {
		if !errors.IsNotFound(vdErr) {
			return fmt.Errorf("failed to check VirtualDisk %s: %w", systemDiskName, vdErr)
		}
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
						Name: cvmiName,
					},
				},
			},
		}
		err := virtClient.VirtualDisks().Create(ctx, systemDisk)
		if err != nil {
			return fmt.Errorf("failed to create VirtualDisk %s: %w", systemDiskName, err)
		}
	}

	// Create VirtualMachine
	_, vmErr := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
	if vmErr != nil {
		if !errors.IsNotFound(vmErr) {
			return fmt.Errorf("failed to check VirtualMachine %s: %w", vmName, vmErr)
		}
		sshPublicKey, err := GetSSHPublicKeyContent()
		if err != nil {
			return fmt.Errorf("failed to get SSH public key content: %w", err)
		}

		// Use setup node cloud-init (with Docker) for bootstrap nodes, regular for others
		var cloudInitData string
		if strings.HasPrefix(vmName, "bootstrap-node-") {
			cloudInitData = generateSetupNodeCloudInit(vmName, sshPublicKey)
		} else {
			cloudInitData = generateCloudInitUserData(vmName, sshPublicKey)
		}

		memoryQuantity := resource.MustParse(fmt.Sprintf("%dGi", node.RAM))
		vm := &v1alpha2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vmName,
				Namespace: namespace,
				Labels:    map[string]string{"vm": "linux", "service": "v1"},
			},
			Spec: v1alpha2.VirtualMachineSpec{
				VirtualMachineClassName:  config.EffectiveVirtualMachineClassName(),
				EnableParavirtualization: true,
				RunPolicy:                v1alpha2.RunPolicy("AlwaysOn"),
				OsType:                   v1alpha2.OsType("Generic"),
				Bootloader:               v1alpha2.BootloaderType("BIOS"),
				LiveMigrationPolicy:      v1alpha2.LiveMigrationPolicy("PreferSafe"),
				CPU: func() v1alpha2.CPUSpec {
					coreFraction := "100%"
					if node.CoreFraction != nil {
						coreFraction = fmt.Sprintf("%d%%", *node.CoreFraction)
					}
					return v1alpha2.CPUSpec{
						Cores:        node.CPU,
						CoreFraction: coreFraction,
					}
				}(),
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
					UserData: cloudInitData,
				},
			},
		}
		err = virtClient.VirtualMachines().Create(ctx, vm)
		if err != nil {
			return fmt.Errorf("failed to create VirtualMachine %s: %w", vmName, err)
		}
	}

	return nil
}

// waitForClusterVirtualImageReady waits for a ClusterVirtualImage to become Ready
func waitForClusterVirtualImageReady(ctx context.Context, virtClient *virtualization.Client, cvmiName string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastLogTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for ClusterVirtualImage %s to become Ready", cvmiName)
		case <-ticker.C:
			cvmi, err := virtClient.ClusterVirtualImages().Get(ctx, cvmiName)
			if err != nil {
				return fmt.Errorf("failed to get ClusterVirtualImage %s: %w", cvmiName, err)
			}

			if cvmi.Status.Phase == "Ready" {
				logger.Debug("ClusterVirtualImage %s is Ready", cvmiName)
				return nil
			}

			if cvmi.Status.Phase == "Failed" {
				return fmt.Errorf("ClusterVirtualImage %s failed to provision", cvmiName)
			}

			if time.Since(lastLogTime) >= 30*time.Second {
				logger.Debug("ClusterVirtualImage %s phase: %s (progress: %s)", cvmiName, cvmi.Status.Phase, cvmi.Status.Progress)
				lastLogTime = time.Now()
			}
		}
	}
}

// getCVMINameFromImageURL extracts a CVMI name from an image URL
// The name must follow RFC 1123 subdomain rules: lowercase alphanumeric, hyphens, dots
// Must start and end with alphanumeric character
func getCVMINameFromImageURL(imageURL string) string {
	// Extract filename from URL and use it as base name
	parts := strings.Split(imageURL, "/")
	filename := parts[len(parts)-1]
	// Remove extension
	name := strings.TrimSuffix(filename, ".img")
	name = strings.TrimSuffix(name, ".qcow2")
	// Make it Kubernetes-friendly (lowercase, replace invalid characters)
	name = strings.ToLower(name)
	// Replace underscores and dots with hyphens (Kubernetes allows hyphens but not underscores)
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	// Remove any consecutive hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	// Ensure it starts and ends with alphanumeric character (RFC 1123 requirement)
	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")
	// If empty after trimming, use a default name
	if name == "" {
		name = "image"
	}
	return name
}

// generateCloudInitUserData generates cloud-init user data for VM provisioning (cluster nodes)
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
  - path: /root/.kubectl_aliases
    content: |
      # kubectl alias and completion
      alias k=kubectl
      complete -o default -F __start_kubectl k

runcmd:
  - systemctl restart ssh
  - hostnamectl set-hostname %s
  - systemctl daemon-reload
  - systemctl enable --now qemu-guest-agent.service
  - echo 'source /root/.kubectl_aliases' >> /root/.bashrc
`, sshPubKey, hostname)
}

// generateSetupNodeCloudInit generates cloud-init user data for the setup/bootstrap node.
// This includes Docker which is required for running the Deckhouse installer.
func generateSetupNodeCloudInit(hostname, sshPubKey string) string {
	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - tmux
  - htop
  - qemu-guest-agent
  - iputils-ping
  - jq
  - curl
  - docker.io

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
  - systemctl enable --now docker.service
`, sshPubKey, hostname)
}

// RemoveAllVMs forcefully stops and deletes virtual machines, virtual disks, and virtual images.
// If a VirtualImage is in use by other resources, it will be skipped but VMs and VDs will still be deleted.
func RemoveAllVMs(ctx context.Context, resources *VMResources) error {
	if resources == nil {
		return fmt.Errorf("resources cannot be nil")
	}

	if len(resources.VMNames) == 0 {
		logger.Skip("No VMs to remove")
		return nil
	}

	// Delete all VMs using RemoveVM
	for i, vmName := range resources.VMNames {
		logger.Progress("Removing VM %d/%d: %s/%s", i+1, len(resources.VMNames), resources.Namespace, vmName)
		err := RemoveVM(ctx, resources.VirtClient, resources.Namespace, vmName)
		if err != nil {
			// Log but continue - we'll try to clean up other VMs
			logger.Error("Failed to remove VM %s/%s: %v", resources.Namespace, vmName, err)
		} else {
			logger.Success("VM %s/%s removed successfully", resources.Namespace, vmName)
		}
	}

	return nil
}

// GetSetupNode returns the setup VM node from ClusterDefinition.
// The setup node is always a separate VM with a unique name (bootstrap-node-<suffix>).
// Note: clusterDef.Setup.Hostname must be set to the generated VM name (done by GatherVMInfo)
func GetSetupNode(clusterDef *config.ClusterDefinition) (*config.ClusterNode, error) {
	if clusterDef == nil {
		return nil, fmt.Errorf("clusterDef cannot be nil")
	}
	if clusterDef.Setup == nil {
		return nil, fmt.Errorf("setup node is not defined in cluster definition")
	}
	return clusterDef.Setup, nil
}

// GetVMIPAddress gets the IP address of a VM by querying its status
// It waits for the VM to have an IP address assigned
// DEPRECATED: Use GatherVMInfo to get all VM info at once, then use VMInfo.GetIPAddress
func GetVMIPAddress(ctx context.Context, virtClient *virtualization.Client, namespace, vmName string) (string, error) {
	vm, err := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
	if err != nil {
		return "", fmt.Errorf("failed to get VM %s/%s: %w", namespace, vmName, err)
	}

	// Get IP from VM status.IPAddress field
	if vm.Status.IPAddress == "" {
		return "", fmt.Errorf("VM %s/%s does not have an IP address in status yet", namespace, vmName)
	}

	return vm.Status.IPAddress, nil
}

// vmIPResult holds the result of fetching an IP address for a VM
type vmIPResult struct {
	node     *config.ClusterNode
	ip       string
	err      error
	hostname string
}

// GatherVMInfoOptions optionally customizes GatherVMInfo behaviour.
// Pass nil for default (gather all VMs including setup).
type GatherVMInfoOptions struct {
	// SkipSetupVM when true skips the setup/bootstrap VM. Use when resuming after Deckhouse is up:
	// the bootstrap node is removed at that point and only master/worker VMs exist.
	SkipSetupVM bool
}

// GatherVMInfo gathers IP addresses for all VMs in the cluster definition and fills them into ClusterDefinition.
// This should be called once while connected to the base cluster, before switching to test cluster.
// It modifies clusterDef in-place by setting IPAddress field for each VM node.
// When opts.SkipSetupVM is true, the setup (bootstrap) VM is not queried and clusterDef.Setup is left unchanged.
func GatherVMInfo(ctx context.Context, virtClient *virtualization.Client, namespace string, clusterDef *config.ClusterDefinition, vmResources *VMResources, opts *GatherVMInfoOptions) error {
	if opts == nil {
		opts = &GatherVMInfoOptions{}
	}
	var wg sync.WaitGroup
	results := make(chan vmIPResult)

	// Gather info for all masters in parallel
	for i := range clusterDef.Masters {
		master := &clusterDef.Masters[i]
		if master.HostType == config.HostTypeVM {
			wg.Add(1)
			go func(node *config.ClusterNode) {
				defer wg.Done()
				ip, err := GetVMIPAddress(ctx, virtClient, namespace, node.Hostname)
				results <- vmIPResult{node: node, ip: ip, err: err, hostname: node.Hostname}
			}(master)
		}
	}

	// Gather info for all workers in parallel
	for i := range clusterDef.Workers {
		worker := &clusterDef.Workers[i]
		if worker.HostType == config.HostTypeVM {
			wg.Add(1)
			go func(node *config.ClusterNode) {
				defer wg.Done()
				ip, err := GetVMIPAddress(ctx, virtClient, namespace, node.Hostname)
				results <- vmIPResult{node: node, ip: ip, err: err, hostname: node.Hostname}
			}(worker)
		}
	}

	// Gather info for setup node unless skipped (e.g. resume: bootstrap VM already removed)
	setupVMName := vmResources.SetupVMName
	if !opts.SkipSetupVM && setupVMName != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, err := GetVMIPAddress(ctx, virtClient, namespace, setupVMName)
			results <- vmIPResult{node: nil, ip: ip, err: err, hostname: setupVMName}
		}()
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var setupIP string
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("failed to get IP for VM %s: %w", result.hostname, result.err)
		}
		if result.node != nil {
			// Master or worker node
			result.node.IPAddress = result.ip
		} else {
			// Setup node
			setupIP = result.ip
		}
	}

	if opts.SkipSetupVM {
		// Do not touch clusterDef.Setup; bootstrap VM is gone
		return nil
	}

	// Create or update clusterDef.Setup with the generated VM info
	if clusterDef.Setup == nil {
		setupNode := config.DefaultSetupVM
		setupNode.Hostname = setupVMName
		setupNode.IPAddress = setupIP
		clusterDef.Setup = &setupNode
	} else {
		clusterDef.Setup.Hostname = setupVMName
		clusterDef.Setup.IPAddress = setupIP
	}

	return nil
}

// GetNodeIPAddress gets the IP address for a node by hostname from ClusterDefinition
func GetNodeIPAddress(clusterDef *config.ClusterDefinition, hostname string) (string, error) {
	// Check masters
	for _, master := range clusterDef.Masters {
		if master.Hostname == hostname {
			if master.IPAddress == "" {
				return "", fmt.Errorf("IP address not set for master node %s", hostname)
			}
			return master.IPAddress, nil
		}
	}

	// Check workers
	for _, worker := range clusterDef.Workers {
		if worker.Hostname == hostname {
			if worker.IPAddress == "" {
				return "", fmt.Errorf("IP address not set for worker node %s", hostname)
			}
			return worker.IPAddress, nil
		}
	}

	// Check setup node
	if clusterDef.Setup != nil && clusterDef.Setup.Hostname == hostname {
		if clusterDef.Setup.IPAddress == "" {
			return "", fmt.Errorf("IP address not set for setup node %s", hostname)
		}
		return clusterDef.Setup.IPAddress, nil
	}

	return "", fmt.Errorf("node with hostname %s not found in cluster definition", hostname)
}

// RemoveVM removes a VM and its associated VirtualDisks, then removes the ClusterVirtualImage if not used by other VMs.
// It removes resources in order: VM -> VirtualDisks -> ClusterVirtualImage (if unused).
func RemoveVM(ctx context.Context, virtClient *virtualization.Client, namespace, vmName string) error {
	// Step 1: Get VM to find associated VirtualDisks
	vm, err := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
	if err != nil {
		if errors.IsNotFound(err) {
			// VM doesn't exist, nothing to clean up
			logger.Skip("VM %s/%s doesn't exist, skipping", namespace, vmName)
			return nil
		}
		return fmt.Errorf("failed to get VM %s/%s: %w", namespace, vmName, err)
	}

	// Collect VirtualDisk names from VM's BlockDeviceRefs
	vdNames := make([]string, 0)
	for _, bdRef := range vm.Spec.BlockDeviceRefs {
		if bdRef.Kind == v1alpha2.DiskDevice {
			vdNames = append(vdNames, bdRef.Name)
		}
	}
	if len(vdNames) > 0 {
		logger.Debug("Found %d VirtualDisk(s) associated with VM: %v", len(vdNames), vdNames)
	}

	// Step 2: Collect ClusterVirtualImage names from VirtualDisks before deleting them
	cvmiNamesSet := make(map[string]bool)
	for _, vdName := range vdNames {
		vd, err := virtClient.VirtualDisks().Get(ctx, namespace, vdName)
		if err != nil {
			if errors.IsNotFound(err) {
				continue // Already deleted
			}
			// Log but continue
			logger.Warn("Failed to get VirtualDisk %s/%s: %v", namespace, vdName, err)
			continue
		}

		if vd.Spec.DataSource != nil && vd.Spec.DataSource.ObjectRef != nil {
			if vd.Spec.DataSource.ObjectRef.Kind == "ClusterVirtualImage" {
				cvmiNamesSet[vd.Spec.DataSource.ObjectRef.Name] = true
			}
		}
	}

	// Step 3: Delete the VM
	logger.Delete("Deleting VirtualMachine %s/%s", namespace, vmName)
	err = virtClient.VirtualMachines().Delete(ctx, namespace, vmName)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete VM %s/%s: %w", namespace, vmName, err)
	}

	// Step 3.5: Wait for VM to be fully deleted before deleting VirtualDisks
	// Kubernetes deletion is asynchronous, so we need to wait until the VM is gone
	logger.Progress("Waiting for VirtualMachine %s/%s to be fully deleted...", namespace, vmName)
	for {
		_, err := virtClient.VirtualMachines().Get(ctx, namespace, vmName)
		if errors.IsNotFound(err) {
			// VirtualMachine is fully deleted
			logger.Success("VirtualMachine %s/%s deleted", namespace, vmName)
			break
		}
		if err != nil {
			// Some other error occurred, log and break to avoid infinite loop
			logger.Warn("Error checking if VirtualMachine %s/%s is deleted: %v", namespace, vmName, err)
			break
		}
		// Wait a bit before checking again
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for VM %s/%s to be deleted: %w", namespace, vmName, ctx.Err())
		case <-time.After(5 * time.Second):
			// Continue polling
		}
	}

	// Step 4: Delete all VirtualDisks associated with this VM
	if len(vdNames) > 0 {
		logger.Delete("Deleting %d VirtualDisk(s)...", len(vdNames))
	}
	deletedVDNames := make(map[string]bool)
	for _, vdName := range vdNames {
		err := virtClient.VirtualDisks().Delete(ctx, namespace, vdName)
		if err != nil && !errors.IsNotFound(err) {
			logger.Error("Failed to delete VirtualDisk %s/%s: %v", namespace, vdName, err)
		} else {
			deletedVDNames[vdName] = true
		}
	}

	// Step 4.5: Wait for all VirtualDisks to be fully deleted before checking ClusterVirtualImage usage
	// Poll until all VirtualDisks we deleted are no longer present
	if len(deletedVDNames) > 0 {
		logger.Progress("Waiting for %d VirtualDisk(s) to be fully deleted...", len(deletedVDNames))
	}
	for len(deletedVDNames) > 0 {
		allDeleted := true
		for vdName := range deletedVDNames {
			_, err := virtClient.VirtualDisks().Get(ctx, namespace, vdName)
			if errors.IsNotFound(err) {
				// VirtualDisk is fully deleted, remove from tracking
				delete(deletedVDNames, vdName)
			} else if err != nil {
				// Some other error occurred, log and remove from tracking to avoid infinite loop
				logger.Warn("Error checking if VirtualDisk %s/%s is deleted: %v", namespace, vdName, err)
				delete(deletedVDNames, vdName)
			} else {
				// VirtualDisk still exists
				allDeleted = false
			}
		}
		if allDeleted {
			if len(vdNames) > 0 {
				logger.Success("All VirtualDisks deleted")
			}
			break
		}
		// Wait a bit before checking again
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for VirtualDisks to be deleted: %w", ctx.Err())
		case <-time.After(5 * time.Second):
			// Continue polling
		}
	}

	// Step 5: Check if ClusterVirtualImages are still in use by other VirtualDisks in the namespace and delete if not
	// Note: Since CVMI is cluster-scoped, it could be used by VDs in other namespaces too,
	// but for simplicity we only check within the current namespace
	if len(cvmiNamesSet) > 0 {
		logger.Debug("Checking ClusterVirtualImage usage (%d image(s))...", len(cvmiNamesSet))
	}
	allVDs, err := virtClient.VirtualDisks().List(ctx, namespace)
	if err != nil {
		logger.Warn("Failed to list VirtualDisks to check ClusterVirtualImage usage: %v", err)
		allVDs = []v1alpha2.VirtualDisk{}
	}

	// Build map of ClusterVirtualImages that are still in use
	cvmiInUse := make(map[string]bool)
	for _, vd := range allVDs {
		if vd.Spec.DataSource != nil && vd.Spec.DataSource.ObjectRef != nil {
			if vd.Spec.DataSource.ObjectRef.Kind == "ClusterVirtualImage" {
				cvmiInUse[vd.Spec.DataSource.ObjectRef.Name] = true
			}
		}
	}

	// Delete ClusterVirtualImages that are not in use (cluster-scoped, no namespace)
	deletedCVMICount := 0
	for cvmiName := range cvmiNamesSet {
		if cvmiInUse[cvmiName] {
			logger.Skip("ClusterVirtualImage %s is still in use, skipping deletion", cvmiName)
			continue // Still in use, skip deletion
		}

		logger.Delete("Deleting ClusterVirtualImage %s", cvmiName)
		err := virtClient.ClusterVirtualImages().Delete(ctx, cvmiName)
		if err != nil && !errors.IsNotFound(err) {
			logger.Error("Failed to delete ClusterVirtualImage %s: %v", cvmiName, err)
		} else {
			deletedCVMICount++
		}
	}
	if deletedCVMICount > 0 {
		logger.Success("Deleted %d ClusterVirtualImage(s)", deletedCVMICount)
	}

	return nil
}

// CleanupSetupVM deletes the setup VM and its associated resources.
// This should be called after the test cluster bootstrap is complete.
// Deprecated: Use RemoveVM instead.
func CleanupSetupVM(ctx context.Context, resources *VMResources) error {
	if resources == nil {
		return fmt.Errorf("resources cannot be nil")
	}

	namespace := resources.Namespace
	setupVMName := resources.SetupVMName

	return RemoveVM(ctx, resources.VirtClient, namespace, setupVMName)
}
