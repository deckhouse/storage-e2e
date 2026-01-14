# Test Template Guide

This guide explains how to use the `test-template` folder to create new E2E tests for Deckhouse storage components.

## Overview

The test template provides a complete framework for creating and managing test clusters. It includes:
- Automatic test cluster creation and configuration
- Module enablement and readiness verification
- Automatic cleanup of resources
- A ready-to-use test structure

## Quick Start

### Automated Method (Recommended)

Use the `create-test.sh` script to automatically create a new test from the template:

```bash
cd tests/
./create-test.sh your-test-name
```

Replace `your-test-name` with a descriptive name for your test (e.g., `storage-class-test`, `volume-snapshot-test`, etc.).

The script will:
- Copy the template folder
- Rename all files appropriately
- Update package names and identifiers
- Create a `test_exports` file for environment variables

**Example:**
```bash
./create-test.sh pvc-resize-test
```

This creates a new test at `tests/pvc-resize-test/` with all files properly configured.

---

### Manual Method

If you prefer to create the test manually, follow these steps:

#### Step 1: Copy the Template Folder

Copy the `test-template` folder to create your new test:

```bash
cd tests/
cp -r test-template your-test-name
```

Replace `your-test-name` with a descriptive name for your test (e.g., `storage-class-test`, `volume-test`, etc.).

### Step 2: Update Package Names

The template uses `test_template` as the package name. You need to update it to match your test folder name.

#### Update `your-test-name_suite_test.go`

1. Rename the file:
   ```bash
   cd your-test-name/
   mv template_suite_test.go your-test-name_suite_test.go
   ```

2. Update the package name and test function:
   ```go
   package your_test_name  // Use underscores, not hyphens
   
   func TestYourTestName(t *testing.T) {  // Update function name
       RegisterFailHandler(Fail)
       suiteConfig, reporterConfig := GinkgoConfiguration()
       reporterConfig.Verbose = true
       reporterConfig.ShowNodeEvents = false
       RunSpecs(t, "Your Test Name Suite", suiteConfig, reporterConfig)  // Update suite name
   }
   ```

#### Update `your-test-name_test.go`

1. Rename the file:
   ```bash
   mv template_test.go your-test-name_test.go
   ```

2. Update the package name:
   ```go
   package your_test_name  // Must match the suite file
   ```

3. Update the Describe block name:
   ```go
   var _ = Describe("Your Test Name", Ordered, func() {
       // ... rest of the code
   })
   ```

### Step 3: Configure Environment Variables

1. Edit the `test_exports` file (automatically created if you used `create-test.sh`) with your values:
   ```bash
   #!/bin/bash
   
   # Required environment variables
   export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
   export DKP_LICENSE_KEY='your-license-key-here'
   export REGISTRY_DOCKER_CFG='your-docker-registry-cfg-here'
   export SSH_USER='your-ssh-user'
   export SSH_HOST='your-ssh-host'
   export TEST_CLUSTER_STORAGE_CLASS='your-storage-class'
   export KUBE_CONFIG_PATH='~/.kube/config'
   export SSH_PASSPHRASE=''  # Optional but required for non-interactive mode
   
   # Optional environment variables with defaults
   export YAML_CONFIG_FILENAME='cluster_config.yml'
   export SSH_PRIVATE_KEY='~/.ssh/id_rsa'
   export SSH_PUBLIC_KEY='~/.ssh/id_rsa.pub'
   export SSH_VM_USER='cloud'
   export TEST_CLUSTER_NAMESPACE='e2e-test-cluster'
   export TEST_CLUSTER_CLEANUP='false'  # Set to 'true' to enable cleanup
   export LOG_LEVEL='debug'  # Set to 'debug' for detailed logs
   ```

2. Source the file to export all environment variables:
   ```bash
   source test_exports
   ```

   **Note:** The `test_exports` file is included in `.gitignore` to prevent accidental commit of sensitive credentials.
