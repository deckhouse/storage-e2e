# E2E Tests

End-to-end tests for Deckhouse storage components.

## Quick Start

1. Create test with script: `cd tests && ./create-test.sh <your-test-name>`
2. Update environment variables in `tests/<your-test-name>/test_exports`
3. Update `tests/<your-test-name>/cluster_config.yml`
4. Apply them: `source tests/<your-test-name>/test_exports`
5. Write your test in `tests/<your-test-name>/<your-test-name>_test.go` (Section marked `---=== TESTS START HERE ===---`)
6. Run the test: `go test -timeout=240m -v ./tests/<your-test-name> -count=1`

The `-count=1` flag prevents Go from using cached test results.
Timeout `240m` is a global timeout for entire testkit. Adjust it on your needs.

### Run a specific test inside testkit

```bash
go test -timeout=240m -v ./tests/test-folder-name -count=1 -ginkgo.focus="should create virtual machines"
```

## Testkits description

### test-template

> NOTE: DO NOT EDIT THIS TESTKIT!

Template folder for creating new E2E tests. Contains a complete framework with:
- Automatic test cluster creation and teardown
- Module enablement and readiness verification
- Environment variable validation and configuration
- Example test structure with BeforeAll/AfterAll hooks

Use `./tests/create-test.sh <your-test-name>` to create a new test from this template.

### csi-all-stress-tests

Stress tests for all CSI storage drivers. This test suite:
- Creates a test cluster with required modules (snapshot-controller, and one or more CSI drivers: csi-huawei, csi-hpe, csi-netapp, csi-s3, etc.)
- Applies CSI custom resources from YAML files in `files/` directory (storage connections, storage classes, NGCs)
- Validates environment variables referenced in CR files are set before applying
- Runs flog stress test with PVC resize operations
- Runs comprehensive snapshot/resize/clone stress test (multiple resize stages, snapshots, clones)

Designed to validate any CSI driver stability under high load with concurrent PVC operations, snapshots, and clones. Configure which drivers to test by editing `crFiles` and `storageClassNames` in the test file.

Run the test: `go test -timeout=120m -v ./tests/csi-all-stress-tests -count=1`

## Environment Variables

### Required

- `SSH_USER` -- SSH username for connecting to the base cluster
- `SSH_HOST` -- SSH host address of the base cluster
- `TEST_CLUSTER_CREATE_MODE` -- Cluster creation mode: `alwaysUseExisting`, `alwaysCreateNew`, or `commander`
- `TEST_CLUSTER_STORAGE_CLASS` -- Storage class for DKP cluster deployment
- `DKP_LICENSE_KEY` -- Deckhouse Platform license key
- `REGISTRY_DOCKER_CFG` -- Docker registry credentials for downloading images from Deckhouse registry

### SSH Configuration

- `SSH_PRIVATE_KEY` -- Path to SSH private key file, or base64-encoded key content. Default: `~/.ssh/id_rsa`. If `SSH_AUTH_SOCK` is set, SSH agent keys are also tried as fallback
- `SSH_PUBLIC_KEY` -- Path to SSH public key file, or plain-text key content. Default: `~/.ssh/id_rsa.pub`
- `SSH_PASSPHRASE` -- Passphrase for the SSH private key. Required for non-interactive mode with encrypted keys
- `SSH_VM_USER` -- SSH user for connecting to VMs deployed inside the test cluster. Default: `cloud`
- `SSH_JUMP_HOST` -- Jump host address for connecting to clusters behind a bastion
- `SSH_JUMP_USER` -- Jump host SSH user. Defaults to `SSH_USER` if jump host is set
- `SSH_JUMP_KEY_PATH` -- Jump host SSH key path. Defaults to `SSH_PRIVATE_KEY` if jump host is set

### Cluster Configuration

- `YAML_CONFIG_FILENAME` -- Filename of the cluster definition YAML. Default: `cluster_config.yml`
- `TEST_CLUSTER_CLEANUP` -- Set to `true` to remove the test cluster after tests complete. Default: `false`
- `TEST_CLUSTER_NAMESPACE` -- Namespace for DKP cluster deployment. Default: `e2e-test-cluster`
- `KUBE_CONFIG_PATH` -- Path to a kubeconfig file. Used as fallback if SSH-based kubeconfig retrieval fails
- `IMAGE_PULL_POLICY` -- Image pull policy for ClusterVirtualImages: `Always` or `IfNotExists`. Default: `IfNotExists`

### Logging

- `LOG_LEVEL` -- Log level: `debug`, `info`, `warn`, or `error`. Default: `debug`
- `LOG_FILE_PATH` -- Path to log file. If set, logs to both console and file
- `LOG_TIMESTAMPS_ENABLED` -- Whether to include timestamps in log output. Default: `true`

### Deckhouse Commander (only when `TEST_CLUSTER_CREATE_MODE=commander`)

- `COMMANDER_URL` -- URL of the Deckhouse Commander API (required)
- `COMMANDER_TOKEN` -- API token for Commander authentication (required)
- `COMMANDER_CLUSTER_NAME` -- Name of the cluster in Commander to use or create. Default: `e2e-test-cluster`
- `COMMANDER_TEMPLATE_NAME` -- Template name for creating a new cluster. Required when `COMMANDER_CREATE_IF_NOT_EXISTS=true`
- `COMMANDER_TEMPLATE_VERSION` -- Template version to use. Defaults to latest
- `COMMANDER_REGISTRY_NAME` -- Registry name for cluster creation (auto-resolved to registry_id)
- `COMMANDER_CREATE_IF_NOT_EXISTS` -- Set to `true` to create a new cluster if it doesn't exist. Default: `false`
- `COMMANDER_WAIT_TIMEOUT` -- Timeout for waiting for cluster to become ready. Default: `30m`
- `COMMANDER_INSECURE_SKIP_TLS_VERIFY` -- Skip TLS certificate verification for Commander API. Default: `false`
- `COMMANDER_CA_CERT` -- Path to CA certificate file for verifying Commander API TLS
- `COMMANDER_AUTH_METHOD` -- Auth method: `x-auth-token`, `bearer`, `token`, `cookie`, or `basic`. Default: `x-auth-token`
- `COMMANDER_AUTH_USER` -- Username for basic authentication (only with `auth_method=basic`)
- `COMMANDER_API_PREFIX` -- API path prefix for Commander API. Default: `/api/v1`
- `COMMANDER_VALUES` -- Template input values for cluster creation as JSON string

### Stress Test Configuration

- `STRESS_TEST_PVC_SIZE` -- Initial PVC size. Default: `100Mi`
- `STRESS_TEST_PODS_COUNT` -- Number of pods to create. Default: `100`
- `STRESS_TEST_PVC_SIZE_AFTER_RESIZE` -- PVC size after first resize. Default: `200Mi`
- `STRESS_TEST_PVC_SIZE_AFTER_RESIZE_STAGE2` -- PVC size after second resize. Default: `300Mi`
- `STRESS_TEST_SNAPSHOTS_PER_PVC` -- Number of snapshots per PVC. Default: `2`
- `STRESS_TEST_MAX_ATTEMPTS` -- Maximum attempts for waiting operations. Default: `360`
- `STRESS_TEST_INTERVAL` -- Interval between attempts in seconds. Default: `5`
- `STRESS_TEST_CLEANUP` -- Whether to cleanup resources after stress tests. Default: `true`
