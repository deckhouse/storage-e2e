# E2E Tests

End-to-end tests for Deckhouse storage components.



## Environment Variables

### Ready-to-use setup script

Copy and customize the following script with your values, put then to `<test-folder>/test_exports`, make executable and run:

```bash
#!/bin/bash

# Required environment variables (must be set)
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'  # or 'alwaysUseExisting'
export DKP_LICENSE_KEY='your-license-key-here'  # Get from license.deckhouse.io
export REGISTRY_DOCKER_CFG='your-docker-registry-cfg-here'  # Get from license.deckhouse.io
export SSH_USER='your-ssh-user'  # SSH username for base cluster connection
export SSH_HOST='your-ssh-host'  # SSH hostname/IP for base cluster
export TEST_CLUSTER_STORAGE_CLASS='your-storage-class'  # Storage class for DVP cluster deployment
export KUBE_CONFIG_PATH='~/.kube/config'  # Local path to kubeconfig for base cluster if SSH retrieval fails
export SSH_PASSPHRASE=''  # Optional but required for non-interactive mode: passphrase for SSH private key

# Optional environment variables with defaults (customize as needed)
export YAML_CONFIG_FILENAME='cluster_config.yml'  # Default: cluster_config.yml
export SSH_PRIVATE_KEY='~/.ssh/id_rsa'  # Default: ~/.ssh/id_rsa
export SSH_PUBLIC_KEY='~/.ssh/id_rsa.pub'
export SSH_VM_USER='cloud'  # Default: cloud
export TEST_CLUSTER_NAMESPACE='e2e-test-cluster'  # Default: e2e-test-cluster
export TEST_CLUSTER_CLEANUP='false'  # Default: false (set to 'true' or 'True' to enable cleanup)

```

## Running Tests

### Run all tests in a test suite

```bash
go test -timeout=90m -v ./tests/cluster-creation-by-steps -count=1
```

The `-count=1` flag prevents Go from using cached test results.

### Run a specific test

```bash
go test -timeout=30m -v ./tests/cluster-creation-by-steps -count=1 -ginkgo.focus="should create virtual machines"
```

## Tests description

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


