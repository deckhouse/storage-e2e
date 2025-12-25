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

package config

// OSTypeMap maps OS type names to their definitions.
//
// TrustIfExists: If ClusterVirtualImage (CVI) already exists in the k8s cluster, reuse it
// instead of treating as a conflict. This allows sharing images across multiple test runs.
//
// CVI naming convention: The CVI name is derived from the image URL filename:
//  1. Extract filename from URL (e.g., "jammy-server-cloudimg-amd64.img")
//  2. Remove extension (.img, .qcow2)
//  3. Convert to lowercase
//  4. Replace underscores and dots with hyphens
//  5. Remove consecutive hyphens
//
// Examples:
//
//	URL: https://cloud-images.ubuntu.com/.../jammy-server-cloudimg-amd64.img
//	CVI name: jammy-server-cloudimg-amd64
//
//	URL: https://.../redos-8-1.x86_64.qcow2
//	CVI name: redos-8-1-x86-64
var OSTypeMap = map[string]OSType{
	"Ubuntu 22.04 6.2.0-39-generic": {
		ImageURL:      "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
		KernelVersion: "6.2.0-39-generic",
		TrustIfExists: true,
	},
	"Ubuntu 24.04 6.8.0-53-generic": {
		ImageURL:      "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		KernelVersion: "6.8.0-53-generic",
		TrustIfExists: true,
	},
	"RedOS 8.0 6.6.26-1.red80.x86_64": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/redos-8-1.x86_64.qcow2",
		KernelVersion: "6.6.26-1.red80.x86_64",
		TrustIfExists: true,
	},
	"RedOS 7.3.6 5.15.78-2.el7.3.x86_64": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/RO732_MIN-STD.qcow2",
		KernelVersion: "5.15.78-2.el7.3.x86_64",
		TrustIfExists: true,
	},
	"Debian 12 Bookworm": {
		ImageURL:      "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
		KernelVersion: "6.2.0",
		TrustIfExists: true,
	},
	"Debian 13 Trixie": {
		ImageURL:      "https://cdimage.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		KernelVersion: "6.8.0",
		TrustIfExists: true,
	},
	"AltLinux 10.4": {
		ImageURL:      "https://ftp.altlinux.org/pub/distributions/ALTLinux/p10/images/cloud/x86_64/alt-server-10.4-p10-cloud-x86_64.qcow2",
		KernelVersion: "6",
		TrustIfExists: true,
	},
	"AltLinux 11": {
		ImageURL:      "https://ftp.altlinux.org/pub/distributions/ALTLinux/p11/images/cloud/x86_64/alt-server-11.0-p11-cloud-x86_64.qcow2",
		KernelVersion: "6",
		TrustIfExists: true,
	},
}

/*
#cloud-config
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
      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQDJ4lrUhqV/ymWyK7rWtx7ulyrUWQqZejmn2pR6/2mxTl+TPUQEYEZKLjt9xtgvOYsfHARRWsoF7URNZdg6LI/HuxMK6kz5ohwrP6GB4XngL7vfyZdefiV6OVK+Fsdw6WgH7Cr5myIc8Sv6gumcDYfT9xX0pcGipRZD9qaHkm34U9jhT6U1QRIgG0Po31HAA6JmKEFZ/0S715McYKTTx3aIFzrm5kxCmNCtk19oMZDOCdYhScVGcZKeaP/PLF7fpvajaWLySwKFfRj1HYnaX1rgmpINNpiWXsq+7D53a7/LUpTIvERYD31fh8YW72hilS8rWbymILZhQFRlTtma0kVY7T5qsvvBmP2da4T5Jn+DqZPI0Ey24eiVO7G8uk0gjZOW8YF5t0OJuVL/0lCBQo3RkIBjg9aR60zaJypVlXRZmYwm4attEjSFOU+4Hymu79NdeJNQhTCAxnCF5NC7OZ7ETtGzEt2L8s2t5w2jRiaDyDzKHeWAgXx7DLYdfqRIO+ETJj5Vmzl/c+R9t0UXNQpTuZjiutukTwVGe+ho/74HuXClUrs6qPkR125KjEHcME+EXuHEkwaCgDGJsCfiecjwFv30E//iPk0weJ3K3wFTyqf2vFixcMDgbkwOjgGqZ005blCfuN+FJ1NbqNLe3YhBymOpLFkB0/DhImqfF4kS6w== korolevn@nikita.korolev-macbook
      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC5U6cSKbJOlgfNa7nhMT/J4PNAXmbfHH6/CA9p53qSmwAgV+shkbS7uEj9W4RxM6Q4dKJeRMLyzHj+76y98OBQ9Sb1PJ0fCDLabZR/NeCWy9m/7Lq2Ti3QMOoBkJurL4+T26edDQfhkivywaSOfrcxDiponDEwcqTQRs2rXQSC0tW0lvQrbYUFOjJZ425OOqUm7KUxuOoNeynLTlS4OerVk0fjTa5EtBuDCbpob47NMYtR+JP4PWOw4H9qyli6kegrW3UqamHpFQAAN+UN0x+KSraHfrF/78HO5IET1BpdHOzP5TAqRNVJySxzVOEl2Nau7cEJiHtqHeaP6/mwO4E699BXNtxWatXxT5dSNxdTwhH7FlpA176h04h5sAooIu3zcA3ItzC78wIrdq7ussDqEQfFcneCIqlMpBI6V/lh+e12uvuj3+PRe6Fekr0DC2QR3+rJsueW7huWHlEXEwrilXy3eVYWIRX+Dihrxd/7KLmnLpBW0hwaWZxTdQflwo7AmJgGD2CAWcKXY1vB6BFsO6Q2MOPlMn+Kejq0YFqguMkIiEMCKKbIkBMeyUdnn03ETaQjAJaANJTcO+RcfP0RVV1C116ePdFu6FumVvSK22pU78p4eIyH1WPpFq4akl2ZFpHVgPQVeO0Yz2TRRjM6Jo9QnaT+XxBi02gpCTY5CQ== user@default
write_files:
  - path: /etc/ssh/sshd_config.d/allow_tcp_forwarding.conf
    content: |
      # Разрешить TCP forwarding
      AllowTcpForwarding yes

runcmd:
  - systemctl restart ssh

  # - curl -fsSL https://download.docker.com/linux/ubuntu/gpg | apt-key add -
  # - add-apt-repository "deb [arch=amd64] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable"
  # - apt-get update -y
  # - apt-get install -y docker-ce docker-ce-cli containerd.io
  # - systemctl start docker
  # - systemctl enable docker

final_message: "🔥🔥🔥 The system is finally up, after $UPTIME seconds 🔥🔥🔥"
*/

/*
#!/bin/bash

# ============================================================================
# Configuration Parameters
# ============================================================================

# Amount of VMs to create
VM_COUNT=1

# Starting index for VM numbering (e.g., 1 for vm-01, vm-02, etc. or 5 for vm-05, vm-06, etc.)
START_INDEX=4

# Namespace
NAMESPACE="ya"

# Cloud init image URL

# RedOS 8:
CLOUD_INIT_IMAGE_URL="https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/redos-8-1.x86_64.qcow2"
VMPREF="red8"

# RedOS 7:
#CLOUD_INIT_IMAGE_URL="https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/RO732_MIN-STD.qcow2"
#VMPREF="red7"

# Ubuntu Server 22.04
#CLOUD_INIT_IMAGE_URL="https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
#VMPREF="ub22"

# Ubuntu 2404 server
#CLOUD_INIT_IMAGE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
#VMPREF="ub24"

# VM name prefix (VM names will be: {PREFIX}{INDEX}, e.g., "vm-01", "vm-02", etc.)
VM_NAME_PREFIX="vm-$VMPREF-$NAMESPACE-"

# Storage class
STORAGE_CLASS="nfs-storage-class"

# CPU configuration
CPU_CORES=4
CPU_CORE_FRACTION="10%"

# RAM
MEMORY_SIZE="8Gi"

# Disk size
DISK_SIZE="60G"

# VirtualMachineClass name (shared across all VMs)
VM_CLASS_NAME="generic2"

# ============================================================================
# Script Logic
# ============================================================================

set -euo pipefail

# Function to format VM number with leading zeros
format_vm_number() {
    local num=$1
    printf "%02d" "$num"
}

# Function to generate VM name from prefix and index
generate_vm_name() {
    local index=$1
    local formatted_index=$(format_vm_number "$index")
    echo "${VM_NAME_PREFIX}${formatted_index}"
}

# Function to generate manifests for a single VM
generate_vm_manifests() {
    local vm_index=$1
    local vm_name=$(generate_vm_name "$vm_index")
    local formatted_index=$(format_vm_number "$vm_index")
    # Extract base name from prefix (remove trailing dash if present)
    local base_name="${VM_NAME_PREFIX%-}"
    local secret_name="${base_name}-cloud-init-${formatted_index}"
    local vd_name="${base_name}-vd-root-${formatted_index}"
    local vi_name="$base_name"


cat <<EOF
---
apiVersion: v1
kind: Secret
metadata:
  name: ${secret_name}
  namespace: ${NAMESPACE}
type: provisioning.virtualization.deckhouse.io/cloud-init
data:
  userData: |
    I2Nsb3VkLWNvbmZpZwpwYWNrYWdlX3VwZGF0ZTogdHJ1ZQpwYWNrYWdlczoKICAtIHRtdX
    gKICAtIGh0b3AKICAtIHFlbXUtZ3Vlc3QtYWdlbnQKICAtIGlwdXRpbHMtcGluZwogIC0g
    c3RyZXNzLW5nCiAgLSBqcQogIC0geXEKICAtIHJzeW5jCiAgLSBmaW8KICAtIGN1cmwKCn
    NzaF9wd2F1dGg6IHRydWUKdXNlcnM6CiAgLSBuYW1lOiBjbG91ZAogICAgIyBwYXNzd2Q6
    IGNsb3VkCiAgICBwYXNzd2Q6ICQ2JHJvdW5kcz00MDk2JHZsbi8uYVBIQk9JN0JNWVIkYk
    JNa3FRdnVHczVHeWQvMUg1RFA0bTlIalFTeS5rZ3J4cGFHRUh3a1g3S0VGVjhCUy5IWldQ
    aXRBdFoyVmQ4WnFJWlJxbWx5a1JDYWdUZ1BlanQxaS4KICAgIHNoZWxsOiAvYmluL2Jhc2
    gKICAgIHN1ZG86IEFMTD0oQUxMKSBOT1BBU1NXRDpBTEwKICAgIGNocGFzc3dkOiB7ZXhw
    aXJlOiBGYWxzZX0KICAgIGxvY2tfcGFzc3dkOiBmYWxzZQogICAgc3NoX2F1dGhvcml6ZW
    Rfa2V5czoKICAgICAgLSBzc2gtcnNhIEFBQUFCM056YUMxeWMyRUFBQUFEQVFBQkFBQUNB
    UURKNGxyVWhxVi95bVd5SzdyV3R4N3VseXJVV1FxWmVqbW4ycFI2LzJteFRsK1RQVVFFWU
    VaS0xqdDl4dGd2T1lzZkhBUlJXc29GN1VSTlpkZzZMSS9IdXhNSzZrejVvaHdyUDZHQjRY
    bmdMN3ZmeVpkZWZpVjZPVksrRnNkdzZXZ0g3Q3I1bXlJYzhTdjZndW1jRFlmVDl4WDBwY0
    dpcFJaRDlxYUhrbTM0VTlqaFQ2VTFRUklnRzBQbzMxSEFBNkptS0VGWi8wUzcxNU1jWUtU
    VHgzYUlGenJtNWt4Q21OQ3RrMTlvTVpET0NkWWhTY1ZHY1pLZWFQL1BMRjdmcHZhamFXTH
    lTd0tGZlJqMUhZbmFYMXJnbXBJTk5waVdYc3ErN0Q1M2E3L0xVcFRJdkVSWUQzMWZoOFlX
    NzJoaWxTOHJXYnltSUxaaFFGUmxUdG1hMGtWWTdUNXFzdnZCbVAyZGE0VDVKbitEcVpQST
    BFeTI0ZWlWTzdHOHVrMGdqWk9XOFlGNXQwT0p1VkwvMGxDQlFvM1JrSUJqZzlhUjYwemFK
    eXBWbFhSWm1Zd200YXR0RWpTRk9VKzRIeW11NzlOZGVKTlFoVENBeG5DRjVOQzdPWjdFVH
    RHekV0Mkw4czJ0NXcyalJpYUR5RHpLSGVXQWdYeDdETFlkZnFSSU8rRVRKajVWbXpsL2Mr
    Ujl0MFVYTlFwVHVaaml1dHVrVHdWR2UraG8vNzRIdVhDbFVyczZxUGtSMTI1S2pFSGNNRS
    tFWHVIRWt3YUNnREdKc0NmaWVjandGdjMwRS8vaVBrMHdlSjNLM3dGVHlxZjJ2Rml4Y01E
    Z2Jrd09qZ0dxWjAwNWJsQ2Z1TitGSjFOYnFOTGUzWWhCeW1PcExGa0IwL0RoSW1xZkY0a1
    M2dz09IGtvcm9sZXZuQG5pa2l0YS5rb3JvbGV2LW1hY2Jvb2sKICAgICAgLSBzc2gtcnNh
    IEFBQUFCM056YUMxeWMyRUFBQUFEQVFBQkFBQUNBUUM1VTZjU0tiSk9sZ2ZOYTduaE1UL0
    o0UE5BWG1iZkhINi9DQTlwNTNxU213QWdWK3Noa2JTN3VFajlXNFJ4TTZRNGRLSmVSTUx5
    ekhqKzc2eTk4T0JROVNiMVBKMGZDRExhYlpSL05lQ1d5OW0vN0xxMlRpM1FNT29Ca0p1ck
    w0K1QyNmVkRFFmaGtpdnl3YVNPZnJjeERpcG9uREV3Y3FUUVJzMnJYUVNDMHRXMGx2UXJi
    WVVGT2pKWjQyNU9PcVVtN0tVeHVPb05leW5MVGxTNE9lclZrMGZqVGE1RXRCdURDYnBvYj
    Q3Tk1ZdFIrSlA0UFdPdzRIOXF5bGk2a2VnclczVXFhbUhwRlFBQU4rVU4weCtLU3JhSGZy
    Ri83OEhPNUlFVDFCcGRIT3pQNVRBcVJOVkp5U3h6Vk9FbDJOYXU3Y0VKaUh0cUhlYVA2L2
    13TzRFNjk5QlhOdHhXYXRYeFQ1ZFNOeGRUd2hIN0ZscEExNzZoMDRoNXNBb29JdTN6Y0Ez
    SXR6Qzc4d0lyZHE3dXNzRHFFUWZGY25lQ0lxbE1wQkk2Vi9saCtlMTJ1dnVqMytQUmU2Rm
    VrcjBEQzJRUjMrckpzdWVXN2h1V0hsRVhFd3JpbFh5M2VWWVdJUlgrRGlocnhkLzdLTG1u
    THBCVzBod2FXWnhUZFFmbHdvN0FtSmdHRDJDQVdjS1hZMXZCNkJGc082UTJNT1BsTW4rS2
    VqcTBZRnFndU1rSWlFTUNLS2JJa0JNZXlVZG5uMDNFVGFRakFKYUFOSlRjTytSY2ZQMFJW
    VjFDMTE2ZVBkRnU2RnVtVnZTSzIycFU3OHA0ZUl5SDFXUHBGcTRha2wyWkZwSFZnUFFWZU
    8wWXoyVFJSak02Sm85UW5hVCtYeEJpMDJncENUWTVDUT09IHVzZXJAZGVmYXVsdAp3cml0
    ZV9maWxlczoKICAtIHBhdGg6IC9ldGMvc3NoL3NzaGRfY29uZmlnLmQvYWxsb3dfdGNwX2
    ZvcndhcmRpbmcuY29uZgogICAgY29udGVudDogfAogICAgICAjINCg0LDQt9GA0LXRiNC4
    0YLRjCBUQ1AgZm9yd2FyZGluZwogICAgICBBbGxvd1RjcEZvcndhcmRpbmcgeWVzCgpydW
    5jbWQ6CiAgLSBzeXN0ZW1jdGwgcmVzdGFydCBzc2gKCiAgIyAtIGN1cmwgLWZzU0wgaHR0
    cHM6Ly9kb3dubG9hZC5kb2NrZXIuY29tL2xpbnV4L3VidW50dS9ncGcgfCBhcHQta2V5IG
    FkZCAtCiAgIyAtIGFkZC1hcHQtcmVwb3NpdG9yeSAiZGViIFthcmNoPWFtZDY0XSBodHRw
    czovL2Rvd25sb2FkLmRvY2tlci5jb20vbGludXgvdWJ1bnR1ICQobHNiX3JlbGVhc2UgLW
    NzKSBzdGFibGUiCiAgIyAtIGFwdC1nZXQgdXBkYXRlIC15CiAgIyAtIGFwdC1nZXQgaW5z
    dGFsbCAteSBkb2NrZXItY2UgZG9ja2VyLWNlLWNsaSBjb250YWluZXJkLmlvCiAgIyAtIH
    N5c3RlbWN0bCBzdGFydCBkb2NrZXIKICAjIC0gc3lzdGVtY3RsIGVuYWJsZSBkb2NrZXIK
    CmZpbmFsX21lc3NhZ2U6ICJcVTAwMDFGNTI1XFUwMDAxRjUyNVxVMDAwMUY1MjUgVGhlIH
    N5c3RlbSBpcyBmaW5hbGx5IHVwLCBhZnRlciAkVVBUSU1FIHNlY29uZHMgXFUwMDAxRjUy
    NVxVMDAwMUY1MjVcVTAwMDFGNTI1Igo=
---
apiVersion: virtualization.deckhouse.io/v1alpha2
kind: VirtualDisk
metadata:
  name: ${vd_name}
  namespace: ${NAMESPACE}
spec:
  dataSource:
    objectRef:
      kind: VirtualImage
      name: ${vi_name}
    type: ObjectRef
  persistentVolumeClaim:
    size: ${DISK_SIZE}
    storageClassName: ${STORAGE_CLASS}
---
apiVersion: virtualization.deckhouse.io/v1alpha2
kind: VirtualImage
metadata:
  name: ${vi_name}
  namespace: ${NAMESPACE}
spec:
  dataSource:
    http:
      url: ${CLOUD_INIT_IMAGE_URL}
    type: HTTP
  storage: ContainerRegistry
---
apiVersion: virtualization.deckhouse.io/v1alpha2
kind: VirtualMachine
metadata:
  name: ${vm_name}
  namespace: ${NAMESPACE}
spec:
  blockDeviceRefs:
  - kind: VirtualDisk
    name: ${vd_name}
  bootloader: BIOS
  cpu:
    coreFraction: ${CPU_CORE_FRACTION}
    cores: ${CPU_CORES}
  disruptions:
    restartApprovalMode: Automatic
  enableParavirtualization: true
  memory:
    size: ${MEMORY_SIZE}
  osType: Generic
  provisioning:
    type: UserDataRef
    userDataRef:
      kind: Secret
      name: ${secret_name}
  runPolicy: AlwaysOn
  virtualMachineClassName: ${VM_CLASS_NAME}
EOF
}

# Function to create VirtualMachineClass (only once, shared across all VMs)
create_vm_class() {
    cat <<EOF | kubectl apply -f -
apiVersion: virtualization.deckhouse.io/v1alpha3
kind: VirtualMachineClass
metadata:
  name: ${VM_CLASS_NAME}
  namespace: ${NAMESPACE}
spec:
  cpu:
    type: Discovery
  nodeSelector: {}
  sizingPolicies:
  - coreFractions:
    - 5%
    - 10%
    - 20%
    - 50%
    - 100%
    cores:
      max: 1024
      min: 1
    dedicatedCores:
    - false
    memory:
      max: 128Gi
      min: 10Mi
EOF
}

# Main execution
main() {
    echo "=========================================="
    echo "Deploying Virtual Machines"
    echo "=========================================="
    echo "VM Count: ${VM_COUNT}"
    echo "Starting Index: ${START_INDEX}"
    echo "Namespace: ${NAMESPACE}"
    echo "VM Name Prefix: ${VM_NAME_PREFIX}"
    echo "Storage Class: ${STORAGE_CLASS}"
    echo "CPU: ${CPU_CORES} cores @ ${CPU_CORE_FRACTION}"
    echo "Memory: ${MEMORY_SIZE}"
    echo "Disk: ${DISK_SIZE}"
    echo "Image URL: ${CLOUD_INIT_IMAGE_URL}"
    echo "=========================================="
    echo ""

    # Create namespace if it doesn't exist
    if ! kubectl get namespace "${NAMESPACE}" &>/dev/null; then
        echo "Creating namespace '${NAMESPACE}'..."
        kubectl create namespace "${NAMESPACE}"
        echo ""
    else
        echo "Namespace '${NAMESPACE}' already exists."
        echo ""
    fi

    # Create VirtualMachineClass if it doesn't exist
    echo "Creating/updating VirtualMachineClass '${VM_CLASS_NAME}'..."
    create_vm_class
    echo ""

    # Create VMs
    local end_index=$((START_INDEX + VM_COUNT - 1))
    for ((i=START_INDEX; i<=end_index; i++)); do
        local vm_name=$(generate_vm_name "$i")
        local vm_number=$((i - START_INDEX + 1))

        echo "Creating VM ${vm_number}/${VM_COUNT}: ${vm_name}..."

        # Generate and apply manifests
        generate_vm_manifests "$i" | kubectl apply -f -

        echo "  Created VirtualMachine: ${vm_name}"
        echo ""
    done

    echo "=========================================="
    echo "Deployment completed successfully!"
    echo "=========================================="
}

# Run main function
main
GiNVxVMDAwMUYMjVcVTAwMDFGNTIIgo
*/
