# E2E Tests

End-to-end tests for Deckhouse storage components.

## Tests

### cluster-creation
High-level test that creates a complete test cluster from a YAML configuration file. This test handles the entire cluster creation process in a single operation.

### cluster-creation-by-steps
Step-by-step test that creates a test cluster incrementally, validating each stage:
1. Environment validation
2. Cluster configuration loading
3. SSH connection establishment
4. Kubeconfig retrieval
5. SSH tunnel setup
6. Virtualization module readiness check
7. Namespace creation
8. Virtual machine creation and provisioning

## Environment Variables

### Required environment variables

- **`TEST_CLUSTER_CREATE_MODE`** - Cluster creation mode. Must be set to either:
  - `alwaysUseExisting` - Use existing cluster
  - `alwaysCreateNew` - Create new cluster

- **`DKP_LICENSE_KEY`** - DKP license key for cluster deployment

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

## Running Tests

### Run all tests in a test suite

```bash
go test -v ./tests/cluster-creation-by-steps -count=1
```

The `-count=1` flag prevents Go from using cached test results.

### Run a specific test

```bash
go test -v ./tests/cluster-creation-by-steps -count=1 -ginkgo.focus="should create virtual machines"
```

### Example with environment variables

```bash
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
export DKP_LICENSE_KEY='your-license-key'
export SSH_PASSPHRASE='your-passphrase'
export TEST_CLUSTER_CLEANUP='true'

go test -v ./tests/cluster-creation-by-steps -count=1
```
