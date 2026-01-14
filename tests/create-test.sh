#!/bin/bash

# Script to create a new E2E test from the test-template
# Usage: ./create-test.sh <test-name>

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored messages
print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_step() {
    echo -e "\n${BLUE}==>${NC} $1"
}

# Check if test name is provided
if [ -z "$1" ]; then
    print_error "Error: Test name is required"
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
    print_error "Error: Test name must contain only lowercase letters, numbers, and hyphens"
    exit 1
fi

# Convert test-name to test_name for package names
PACKAGE_NAME="${TEST_NAME//-/_}"

# Convert test-name to TestName for function names (capitalize each word)
FUNCTION_NAME=$(echo "$TEST_NAME" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) tolower(substr($i,2));}1' | sed 's/ //g')

# Convert test-name to "Test Name" for display names
DISPLAY_NAME=$(echo "$TEST_NAME" | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2);}1')

print_step "Creating new test: ${TEST_NAME}"
print_info "Package name: ${PACKAGE_NAME}"
print_info "Function name: Test${FUNCTION_NAME}"
print_info "Display name: ${DISPLAY_NAME}"
echo ""

# Check if template directory exists
if [ ! -d "$TEMPLATE_DIR" ]; then
    print_error "Error: Template directory not found at: $TEMPLATE_DIR"
    exit 1
fi

# Check if target directory already exists
if [ -d "$TARGET_DIR" ]; then
    print_error "Error: Test directory already exists: $TARGET_DIR"
    echo ""
    read -p "Do you want to remove it and continue? (yes/no): " confirm
    if [ "$confirm" != "yes" ]; then
        print_info "Operation cancelled"
        exit 1
    fi
    rm -rf "$TARGET_DIR"
    print_warning "Removed existing directory"
fi

# Step 1: Copy template
print_step "Step 1: Copying template folder"
cp -r "$TEMPLATE_DIR" "$TARGET_DIR"
print_success "Template copied to: $TARGET_DIR"

# Step 2: Rename files
print_step "Step 2: Renaming files"

# Rename suite test file
if [ -f "${TARGET_DIR}/template_suite_test.go" ]; then
    mv "${TARGET_DIR}/template_suite_test.go" "${TARGET_DIR}/${PACKAGE_NAME}_suite_test.go"
    print_success "Renamed template_suite_test.go → ${PACKAGE_NAME}_suite_test.go"
else
    print_warning "template_suite_test.go not found, skipping"
fi

# Rename main test file
if [ -f "${TARGET_DIR}/template_test.go" ]; then
    mv "${TARGET_DIR}/template_test.go" "${TARGET_DIR}/${PACKAGE_NAME}_test.go"
    print_success "Renamed template_test.go → ${PACKAGE_NAME}_test.go"
else
    print_warning "template_test.go not found, skipping"
fi

# Step 3: Update file contents
print_step "Step 3: Updating package names and identifiers"

# Function to update file content
update_file() {
    local file="$1"
    if [ ! -f "$file" ]; then
        print_warning "File not found: $file, skipping"
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
    
    print_success "Updated $(basename "$file")"
}

# Update suite test file
update_file "${TARGET_DIR}/${PACKAGE_NAME}_suite_test.go"

# Update main test file
update_file "${TARGET_DIR}/${PACKAGE_NAME}_test.go"

# Step 4: Create test_exports file if it doesn't exist
print_step "Step 4: Creating test_exports file"

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
    print_success "Created test_exports file (remember to update with your values)"
else
    print_info "test_exports file already exists, not overwriting"
fi

# Final summary
print_step "Test creation complete!"
echo ""
print_success "New test created at: ${TARGET_DIR}"
echo ""
print_info "Next steps:"
echo "  1. cd ${TEST_NAME}"
echo "  2. Edit test_exports with your environment variables"
echo "  3. Edit cluster_config.yml if needed"
echo "  4. Implement your tests in ${PACKAGE_NAME}_test.go"
echo "  5. Run: source test_exports && go test -v -timeout=60m"
echo ""
print_info "Files created:"
echo "  • ${PACKAGE_NAME}_suite_test.go"
echo "  • ${PACKAGE_NAME}_test.go"
echo "  • cluster_config.yml"
echo "  • test_exports"
echo ""
print_warning "Remember to update test_exports with your actual credentials before running tests!"
