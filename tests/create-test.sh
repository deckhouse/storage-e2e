#!/bin/bash

# Script to create a new E2E test from the test-template
# Usage: ./create-test.sh <test-name>

set -e

# Check if test name is provided
if [ -z "$1" ]; then
    echo "Error: Test name is required" >&2
    echo ""
    echo "Usage: $0 <test-name>"
    echo ""
    echo "Example: $0 storage-class-test"
    echo "         $0 volume-snapshot-test"
    exit 1
fi

TEST_NAME="$1"
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_DIR="${SCRIPT_DIR}/test-template"
TARGET_DIR="${SCRIPT_DIR}/${TEST_NAME}"

# Validate test name (alphanumeric and hyphens only)
if ! [[ "$TEST_NAME" =~ ^[a-z0-9-]+$ ]]; then
    echo "Error: Test name must contain only lowercase letters, numbers, and hyphens" >&2
    exit 1
fi

# Convert test-name to test_name for package names
PACKAGE_NAME="${TEST_NAME//-/_}"

# Convert test-name to TestName for function names (capitalize each word)
FUNCTION_NAME=$(echo "$TEST_NAME" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2));}1' | sed 's/ //g')

# Convert test-name to "Test Name" for display names
DISPLAY_NAME=$(echo "$TEST_NAME" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2);}1')

# Check if template directory exists
if [ ! -d "$TEMPLATE_DIR" ]; then
    echo "Error: Template directory not found at: $TEMPLATE_DIR" >&2
    exit 1
fi

# Check if target directory already exists
if [ -d "$TARGET_DIR" ]; then
    echo "Error: Test directory already exists: $TARGET_DIR" >&2
    exit 1
fi

# Step 1: Copy template
cp -r "$TEMPLATE_DIR" "$TARGET_DIR"

# Step 2: Rename files
# Rename suite test file
if [ -f "${TARGET_DIR}/template_suite_test.go" ]; then
    mv "${TARGET_DIR}/template_suite_test.go" "${TARGET_DIR}/${PACKAGE_NAME}_suite_test.go"
fi

# Rename main test file
if [ -f "${TARGET_DIR}/template_test.go" ]; then
    mv "${TARGET_DIR}/template_test.go" "${TARGET_DIR}/${PACKAGE_NAME}_test.go"
fi

# Step 3: Update file contents
update_file() {
    local file="$1"
    if [ ! -f "$file" ]; then
        return
    fi
    
    # Create a temporary file
    local temp_file="${file}.tmp"
    
    # Perform replacements using sed
    sed \
        -e "s/package test_template/package ${PACKAGE_NAME}/g" \
        -e "s/func TestTemplate(/func Test${FUNCTION_NAME}(/g" \
        -e "s/\"Template Test Suite\"/\"${DISPLAY_NAME} Suite\"/g" \
        -e "s/Template Test/${DISPLAY_NAME}/g" \
        "$file" > "$temp_file"
    
    # Replace original file
    mv "$temp_file" "$file"
}

# Update suite test file
update_file "${TARGET_DIR}/${PACKAGE_NAME}_suite_test.go"

# Update main test file
update_file "${TARGET_DIR}/${PACKAGE_NAME}_test.go"

# Step 4: Create test_exports file if it doesn't exist
if [ ! -f "${TARGET_DIR}/test_exports" ]; then
    cat > "${TARGET_DIR}/test_exports" << 'EOF'
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
export TEST_CLUSTER_CLEANUP='false'  # Set to 'true' to enable cleanup after tests
export LOG_LEVEL='debug'  # Set to 'debug' for detailed logs, 'info' for normal logs
EOF
    chmod +x "${TARGET_DIR}/test_exports"
fi
