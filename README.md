# E2E Tests

End-to-end tests for Deckhouse storage components.

## Tests

### cluster-creation
High-level test that creates a complete test cluster from a YAML configuration file. This test handles the entire cluster creation process in a single operation.

### cluster-creation-by-steps
Step-by-step test that creates a test cluster incrementally, validating each stage:

**Setup (BeforeAll):**
1. Environment validation - Validates required environment variables are set
2. Cluster configuration loading - Loads and parses cluster definition from YAML file

**Test Steps:**
1. Connect to base cluster - Establishes SSH connection, retrieves kubeconfig, and sets up port forwarding tunnel
2. Virtualization module readiness check - Verifies virtualization module is Ready
3. Test namespace creation - Creates test namespace if it doesn't exist
4. Virtual machine creation and provisioning - Creates VMs and waits for them to become Running
5. VM information gathering - Gathers IP addresses and other information for all VMs
6. SSH connection establishment to setup node (through base cluster master) - Connects to setup node via jump host
7. Docker installation on setup node - Installs Docker (required for DKP bootstrap)
8. Bootstrap configuration preparation - Prepares bootstrap config from template with cluster-specific values
9. Bootstrap files upload (private key and config.yml) to setup node - Uploads files needed for DKP bootstrap
10. Cluster bootstrap - Bootstraps Kubernetes cluster from setup node to first master node
11. NodeGroup creation for workers - Creates static NodeGroup for worker nodes
12. Cluster readiness verification - Verifies cluster is ready by checking deckhouse deployment
13. Node addition to cluster - Adds remaining master nodes and all worker nodes to the cluster
14. Module enablement and configuration - Enables and configures modules from cluster definition
15. Module readiness verification - Waits for all modules to become Ready in the test cluster

## Environment Variables

### Ready-to-use setup script

Copy and customize the following script with your values:

```bash
#!/bin/bash

# Required environment variables (must be set)
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'  # or 'alwaysUseExisting'
export DKP_LICENSE_KEY='your-license-key-here'  # Get from license.deckhouse.io
export REGISTRY_DOCKER_CFG='your-docker-registry-cfg-here'  # Get from license.deckhouse.io
export SSH_USER='your-ssh-user'  # SSH username for base cluster connection
export SSH_HOST='your-ssh-host'  # SSH hostname/IP for base cluster

# Optional environment variables with defaults (customize as needed)
export YAML_CONFIG_FILENAME='cluster_config.yml'  # Default: cluster_config.yml
export SSH_KEY_PATH='~/.ssh/id_rsa'  # Default: ~/.ssh/id_rsa
export SSH_PASSPHRASE=''  # Optional: passphrase for SSH private key
export SSH_VM_USER='cloud'  # Default: cloud
export SSH_VM_PUBLIC_KEY='ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC8WyGvnBNQp+v6CUweF1QYCRtR7Do/IA8IA2uMd2HuBsddFrc5xYon2ZtEvypZC4Vm1CzgcgUm9UkHgxytKEB4zOOWkmqFP62OSLNyuWMaFEW1fb0EDenup6B5SrjnA8ckm4Hf2NSLvwW9yS98TfN3nqPOPJKfQsN+OTiCerTtNyXjca//ppuGKsQd99jG7SqE9aDQ3sYCXatM53SXqhxS2nTew82bmzVmKXDxcIzVrS9f+2WmXIdY2cKo2I352yKWOIp1Nk0uji8ozLPHFQGvbAG8DGG1KNVcBl2qYUcttmCpN+iXEcGqyn/atUVJJMnZXGtp0fiL1rMLqAd/bb6TFNzZFSsS+zqGesxqLePe32vLCQ3xursP3BRZkrScM+JzIqevfP63INHJEZfYlUf4Ic+gfliS2yA1LwhU7hD4LSVXMQynlF9WeGjuv6ZYxmO8hC6IWCqWnIUqKUiGtvBSPXwsZo7wgljBr4ykJgBzS9MjZ0fzz1JKe80tH6clpjIOn6ReBPwQBq2zmDDrpa5GVqqqjXhRQuA0AfpHdhs5UKxs1PBr7/PTLA7PI39xkOAE/Zj1TYQ2dmqvpskshi7AtBStjinQBAlLXysLSHBtO+3+PLAYcMZMVfb0bVqfGGludO2prvXrrWWTku0eOsA5IRahrRdGhv5zhKgFV7cwUQ== ayakubov@MacBook-Pro-Alexey.local'  # Default: hardcoded key
export TEST_CLUSTER_NAMESPACE='e2e-test-cluster'  # Default: e2e-test-cluster
export TEST_CLUSTER_STORAGE_CLASS='rsc-test-r2-local'  # Default: rsc-test-r2-local
export TEST_CLUSTER_CLEANUP='false'  # Default: false (set to 'true' or 'True' to enable cleanup)
export KUBE_CONFIG_PATH=''  # Optional: fallback path to kubeconfig if SSH retrieval fails
```

**Note:** The `SSH_VM_PUBLIC_KEY` default value is a hardcoded public key. You can replace it with your own SSH public key if needed.

## Configuration Parameters

These are code-level configuration constants defined in `internal/config/config.go`:

- **`DefaultSetupVM`** - Default configuration for the setup/bootstrap VM node:
  - Hostname prefix: `bootstrap-node-`
  - Host type: VM
  - Role: setup
  - OS Type: Ubuntu 22.04 6.2.0-39-generic
  - CPU: 2 cores
  - RAM: 4 GB
  - Disk size: 20 GB

- **`VMsRunningTimeout`** - Timeout for waiting for all VMs to become Running state (default: `20 minutes`)

**Note:** When running tests, use `-timeout` flag that is longer than `VMsRunningTimeout` to allow enough time for VM provisioning. For example, use `-timeout=25m` or `-timeout=60m` to ensure the test doesn't timeout prematurely.

## Running Tests

### Run all tests in a test suite

```bash
go test -timeout=60m -v ./tests/cluster-creation-by-steps -count=1
```

The `-count=1` flag prevents Go from using cached test results.

### Run a specific test

```bash
go test -timeout=60m -v ./tests/cluster-creation-by-steps -count=1 -ginkgo.focus="should create virtual machines"
```

### Example with environment variables

```bash
# Source the setup script (or copy the exports from above)
source setup_env.sh  # if you saved the script above

# Or set variables inline
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
export DKP_LICENSE_KEY='your-license-key'
export REGISTRY_DOCKER_CFG='your-docker-registry-cfg'
export SSH_USER='your-ssh-user'
export SSH_HOST='your-ssh-host'
export SSH_PASSPHRASE='your-passphrase'
export TEST_CLUSTER_CLEANUP='true'

go test -timeout=60m -v ./tests/cluster-creation-by-steps -count=1
```
