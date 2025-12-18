# E2E Tests

End-to-end tests for Deckhouse storage components.

## Tests

### cluster-creation
High-level test that creates a complete test cluster from a YAML configuration file. This test handles the entire cluster creation process in a single operation.

### cluster-creation-by-steps
Step-by-step test that creates a test cluster incrementally, validating each stage:

1. Environment validation - Validates required environment variables are set
2. Cluster configuration loading - Loads and parses cluster definition from YAML file
3. SSH connection establishment to base cluster - Connects to base cluster via SSH
4. Kubeconfig retrieval from base cluster - Fetches kubeconfig file from base cluster
5. SSH tunnel setup with port forwarding - Establishes tunnel to access Kubernetes API
6. Virtualization module readiness check - Verifies virtualization module is Ready
7. Test namespace creation - Creates test namespace if it doesn't exist
8. Virtual machine creation and provisioning - Creates VMs and waits for them to become Running
9. SSH connection establishment to setup node (through base cluster master) - Connects to setup node via jump host
10. Docker installation on setup node - Installs Docker (required for DKP bootstrap)
11. Bootstrap configuration preparation - Prepares bootstrap config from template with cluster-specific values
12. Bootstrap files upload (private key and config.yml) to setup node - Uploads files needed for DKP bootstrap

## Environment Variables

### Required environment variables

- **`TEST_CLUSTER_CREATE_MODE`** - Cluster creation mode. Must be set to either:
  - `alwaysUseExisting` - Use existing cluster
  - `alwaysCreateNew` - Create new cluster

- **`DKP_LICENSE_KEY`** - DKP license key for cluster deployment (see license token at license.deckhouse.io)

- **`REGISTRY_DOCKER_CFG`** - dockerRegistryCfg for downloading images from Deckhouse registry (see license.deckhouse.io)

### Optional (with defaults)

- **`YAML_CONFIG_FILENAME`** - YAML configuration file name (default: `cluster_config.yml`)

- **`SSH_USER`** - SSH username for base cluster connection (default: `a.yakubov`)
- **`SSH_HOST`** - SSH hostname/IP for base cluster (default: `94.26.231.181`)
- **`SSH_KEY_PATH`** - Path to SSH private key (default: `~/.ssh/id_rsa`)
- **`SSH_PASSPHRASE`** - Passphrase for SSH private key (no default)

- **`SSH_VM_USER`** - SSH username for VM access (default: `cloud`)
- **`SSH_VM_PUBLIC_KEY`** - SSH public key to deploy to VMs (default: hardcoded key)

- **`TEST_CLUSTER_NAMESPACE`** - Namespace for test cluster deployment (default: `e2e-test-cluster`)
- **`TEST_CLUSTER_STORAGE_CLASS`** - Storage class for test cluster (default: `rsc-test-r2-local`)
- **`TEST_CLUSTER_CLEANUP`** - Whether to cleanup test cluster after tests (default: `false`, set to `true` or `True` to enable)

- **`KUBE_CONFIG_PATH`** - Fallback path to kubeconfig file if SSH retrieval fails (no default)

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
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
export DKP_LICENSE_KEY='your-license-key'
export REGISTRY_DOCKER_CFG='base64-encoded-docker-config-json'
export SSH_PASSPHRASE='your-passphrase'
export TEST_CLUSTER_CLEANUP='true'

go test -timeout=60m -v ./tests/cluster-creation-by-steps -count=1
```
