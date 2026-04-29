# E2E Test Framework Architecture

## Executive Summary

This document describes the current architecture of the E2E test framework for Deckhouse storage components. The framework provides a modular, maintainable structure for creating and running end-to-end tests with automatic cluster lifecycle management.

**Key Features:**
- Modular package structure with clear separation of concerns
- Automatic test cluster creation and teardown
- Comprehensive logging with configurable levels
- Template-based test creation via `create-test.sh` script
- Environment-based configuration management

---

## 1. Current Architecture

### 1.1 Package Structure

```
storage-e2e/
├── internal/                      # Internal packages (not importable outside module)
│   ├── config/                    # Configuration management
│   │   ├── config.go             # Main configuration struct
│   │   ├── env.go                # Environment variable parsing
│   │   ├── types.go              # Configuration type definitions
│   │   └── images.go             # OS image definitions
│   │
│   ├── cluster/                   # Cluster lifecycle management
│   │   └── cluster.go            # Core cluster operations (kubeconfig, port patching)
│   │
│   ├── kubernetes/                # Kubernetes API operations
│   │   ├── commander/            # Deckhouse Commander HTTP API client
│   │   │   ├── client.go
│   │   │   ├── errors.go
│   │   │   └── types.go
│   │   ├── deckhouse/            # Deckhouse CRDs (Module, ModuleConfig, etc.)
│   │   │   ├── client.go
│   │   │   ├── modules.go
│   │   │   ├── nodegroups.go
│   │   │   └── types.go
│   │   ├── storage/              # SDS node-configurator CRDs
│   │   │   ├── blockdevice.go
│   │   │   └── lvmvolumegroup.go
│   │   └── virtualization/       # Virtualization resources
│   │       ├── client.go
│   │       ├── virtual_machine.go
│   │       ├── virtual_disk.go
│   │       ├── virtual_image.go
│   │       ├── cluster_virtual_image.go
│   │       └── vm_block_device.go
│   │
│   ├── infrastructure/            # Infrastructure layer
│   │   └── ssh/                  # SSH operations
│   │       ├── client.go
│   │       ├── interface.go
│   │       ├── tunnel.go
│   │       └── types.go
│   │
│   └── logger/                    # Structured logging
│       ├── logger.go             # Logger implementation
│       ├── handler.go            # Custom console handler
│       ├── level.go              # Log level parsing
│       ├── config.go             # Logger configuration
│       ├── multi_handler.go      # Multi-handler support
│       └── README.md             # Logging documentation
│
├── pkg/                           # Public API (importable by external packages)
│   ├── cluster/                  # Public cluster management API
│   │   ├── cluster.go            # Main cluster creation/management
│   │   ├── setup.go              # Cluster setup and bootstrap operations
│   │   ├── lock.go               # Cluster locking (ConfigMap-based)
│   │   └── vms.go                # VM lifecycle management
│   │
│   ├── kubernetes/               # Public Kubernetes utilities
│   │   ├── apply.go              # YAML manifest application
│   │   ├── blockdevice.go        # BlockDevice operations
│   │   ├── cephblockpool.go      # Rook CephBlockPool operations
│   │   ├── cephcluster.go        # Rook CephCluster operations
│   │   ├── cephclusterconnection.go # csi-ceph connection/auth CRs
│   │   ├── cephcredentials.go    # Rook Ceph credential discovery
│   │   ├── cephstorageclass.go   # csi-ceph CephStorageClass CR
│   │   ├── client.go             # Clientset/dynamic client with retry
│   │   ├── localstorageclass.go  # LocalStorageClass CR operations
│   │   ├── lvmvolumegroup.go     # LVMVolumeGroup operations
│   │   ├── modules.go            # Module configuration and readiness
│   │   ├── namespace.go          # Namespace utilities
│   │   ├── nodegroup.go          # NodeGroup operations
│   │   ├── nodes.go              # Node listing, taints, labels
│   │   ├── pod.go                # Pod operations
│   │   ├── pvc.go                # PVC operations
│   │   ├── rookconfigoverride.go # Rook ceph.conf override ConfigMap
│   │   ├── secrets.go            # Secret operations
│   │   ├── storageclass.go       # StorageClass get/wait/default
│   │   ├── storageclass_manage.go # Global default StorageClass management
│   │   ├── virtualdisk.go        # VirtualDisk attach/detach
│   │   ├── vmpod.go              # VM pod lookup
│   │   └── volumesnapshotclass.go # VolumeSnapshotClass helpers
│   │
│   ├── retry/                    # Generic retry with exponential backoff
│   │   └── retry.go
│   │
│   └── testkit/                  # Test framework utilities
│       ├── ceph.go               # EnsureCephStorageClass (Rook + csi-ceph)
│       ├── ceph_cluster.go       # EnsureCephCluster (Rook only, no csi-ceph)
│       ├── ceph_crc.go           # Ceph CRC tuning helpers
│       ├── storageclass.go       # EnsureDefaultStorageClass (sds-local-volume)
│       └── stress-tests.go       # Stress test runner
│
├── tests/                         # Test suites
│   ├── test-template/            # Template for creating new tests
│   │   ├── template_suite_test.go
│   │   ├── template_test.go
│   │   └── cluster_config.yml
│   │
│   ├── csi-all-stress-tests/     # CSI stress tests
│   │   ├── csi_all_stress_tests_suite_test.go
│   │   ├── csi_all_stress_tests_test.go
│   │   ├── cluster_config.yml
│   │   └── files/                # CSI CR YAML files and scripts
│   │
│   └── create-test.sh            # Script to create new tests from template
│
├── docs/                          # Documentation
│   ├── ARCHITECTURE.md           # This file
│   ├── FUNCTIONS_GLOSSARY.md     # Exported functions reference
│   ├── TODO.md                   # Global TODO
│   └── WORKLOG.md                # Change log
│
├── files/                         # Static files and templates
│   └── bootstrap/
│       └── config.yml.tpl        # Bootstrap configuration template
│
├── go.mod
├── go.sum
├── README.md                      # Main documentation
└── LICENSE
```

### 1.2 Layer Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Test Layer                            │
│  (tests/*.go - High-level test scenarios)               │
└──────────────────┬──────────────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────────────┐
│              Testkit API Layer                           │
│  (pkg/testkit/* - Public test helpers and fixtures)     │
└──────────────────┬──────────────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────────────┐
│            Domain Logic Layer                            │
│  (internal/cluster, internal/kubernetes/*)              │
│  - Cluster management                                    │
│  - Resource operations                                   │
│  - Business logic                                        │
└──────────────────┬──────────────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────────────┐
│         Infrastructure Layer                             │
│  (internal/infrastructure/*)                            │
│  - SSH connections                                       │
│  - VM provisioning                                       │
│  - Network tunneling                                     │
└──────────────────┬──────────────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────────────┐
│         Kubernetes API Layer                             │
│  (k8s.io/client-go, controller-runtime)                 │
└──────────────────────────────────────────────────────────┘
```

### 1.3 Core Components

#### Test Cluster Management

The framework provides automated test cluster lifecycle management through `pkg/cluster`:

```go
// pkg/cluster/cluster.go
type TestClusterResources struct {
    SSHClient          ssh.SSHClient
    Kubeconfig         *rest.Config
    KubeconfigPath     string
    TunnelInfo         *ssh.TunnelInfo
    ClusterDefinition  *config.ClusterDefinition
    VMResources        *VMResources
    
    // Base cluster resources (for cleanup)
    BaseClusterClient  ssh.SSHClient
    BaseKubeconfig     *rest.Config
    BaseKubeconfigPath string
    BaseTunnelInfo     *ssh.TunnelInfo
    SetupSSHClient     ssh.SSHClient
}

// Main functions for creating new clusters
func CreateTestCluster(ctx context.Context, configPath string) (*TestClusterResources, error)
func WaitForTestClusterReady(ctx context.Context, resources *TestClusterResources) error
func CleanupTestCluster(ctx context.Context, resources *TestClusterResources) error

// Functions for using existing clusters
func UseExistingCluster(ctx context.Context) (*TestClusterResources, error)
func CleanupExistingCluster(ctx context.Context, resources *TestClusterResources) error

// Cluster locking functions (for exclusive access in alwaysUseExisting mode)
func AcquireClusterLock(ctx context.Context, kubeconfig *rest.Config, testName string) error
func ReleaseClusterLock(ctx context.Context, kubeconfig *rest.Config) error
func IsClusterLocked(ctx context.Context, kubeconfig *rest.Config) (bool, error)
func GetClusterLockInfo(ctx context.Context, kubeconfig *rest.Config) (*ClusterLockInfo, error)
```

#### Cluster Locking Mechanism

When using the `alwaysUseExisting` mode, the framework implements a cluster locking mechanism to ensure exclusive access:

- **Lock Location**: A ConfigMap named `e2e-cluster-lock` is created in the `default` namespace
- **Lock Information**: The ConfigMap contains:
  - `test-name`: Name of the test holding the lock
  - `locked-at`: Timestamp when the lock was acquired
  - `locked-by`: Username of the person running the test
  - `hostname`: Hostname of the machine running the test
  - `pid`: Process ID of the test process
- **Automatic Cleanup**: The lock is automatically released when `CleanupExistingCluster` is called
- **Error Handling**: If a test tries to acquire a lock on an already-locked cluster, it receives a detailed error message with lock information

#### Configuration Management

Configuration is managed through environment variables validated in `internal/config`:

```go
// internal/config/env.go - Key environment variables
const (
    // Cluster creation modes
    ClusterCreateModeAlwaysUseExisting = "alwaysUseExisting"
    ClusterCreateModeAlwaysCreateNew   = "alwaysCreateNew"
    
    // Log levels
    LogLevelDebug = "debug"
    LogLevelInfo  = "info"
    LogLevelWarn  = "warn"
    LogLevelError = "error"
)

// Required environment variables
var (
    SSHUser               string  // SSH username for base cluster
    SSHHost               string  // SSH host for base cluster
    TestClusterStorageClass string  // Storage class for test cluster VMs
    DKPLicenseKey         string  // Deckhouse license key
    RegistryDockerCfg     string  // Docker registry credentials
    TestClusterCreateMode string  // Cluster creation mode
    // ... more variables
)

func ValidateEnvironment() error {
    // Validates all required variables and sets defaults
}
```

#### Logging System

The framework includes a structured logging system in `internal/logger`:

```go
// Logging functions with emoji indicators
logger.Step(step int, format string, args ...interface{})      // ▶️ Major steps
logger.StepComplete(step int, format string, args ...interface{}) // ✅ Step completion
logger.Success(format string, args ...interface{})              // ✅ Success (DEBUG)
logger.Info(format string, args ...interface{})                 // Info messages
logger.Warn(format string, args ...interface{})                 // ⚠️ Warnings
logger.Error(format string, args ...interface{})                // ❌ Errors
logger.Debug(format string, args ...interface{})                // 🔧 Debug info
logger.Progress(format string, args ...interface{})             // ⏳ Progress (DEBUG)
```

Log levels are controlled via `LOG_LEVEL` environment variable.

---

## 2. Creating New Tests

### 2.1 Test Creation Workflow

The framework provides an automated script to create new tests from a template:

```bash
cd tests/
./create-test.sh <your-test-name>
```

This script:
1. Copies the `test-template` folder
2. Renames files appropriately
3. Updates package names and identifiers
4. Creates a `test_exports` file for environment variables

### 2.2 Test Template Structure

```
test-template/
├── template_suite_test.go    # Ginkgo suite setup (BeforeSuite/AfterSuite)
├── template_test.go           # Test implementation (BeforeAll/AfterAll/It)
├── cluster_config.yml         # Cluster configuration (VMs, modules, etc.)
└── test_exports               # Environment variables template
```

### 2.3 Test Lifecycle Hooks

Tests use Ginkgo's lifecycle hooks:

**BeforeSuite** (runs once before all specs):
- Validates environment variables
- Initializes logger with configured log level

**BeforeAll** (runs once before ordered container):
- Outputs environment configuration
- Creates test cluster (automatically waits for modules to be ready during creation)

**AfterAll** (runs after all tests in container):
- Cleans up test cluster resources
- Removes VMs based on `TEST_CLUSTER_CLEANUP` setting

**Test Execution**:
- First `It` block: Creates test cluster (modules are automatically configured and waited for)
- Subsequent `It` blocks: Run actual tests against the cluster

---

## 3. Module Details

### 3.1 Configuration Module (`internal/config/`)

```
config/
├── config.go           # Main configuration operations
├── env.go              # Environment variable definitions and validation
├── types.go            # Configuration type definitions
└── images.go           # OS image URL definitions
```

**Responsibilities**:
- Environment variable parsing and validation
- Configuration validation with clear error messages
- Default value management
- Type-safe configuration access

**Key Features**:
- Validates required vs optional variables
- Provides helpful error messages for missing configuration
- Sets sensible defaults for optional values
- Supports multiple cluster creation modes

### 3.2 Cluster Module (`pkg/cluster/` and `internal/cluster/`)

```
pkg/cluster/
├── cluster.go          # Main cluster lifecycle functions
├── setup.go            # Cluster setup and bootstrap
├── lock.go             # Cluster locking (ConfigMap-based)
└── vms.go              # VM lifecycle management

internal/cluster/
└── cluster.go          # Internal cluster operations (config loading, kubeconfig management)
```

**Responsibilities**:
- Complete cluster lifecycle management (create, ready, cleanup)
- VM provisioning and configuration
- Deckhouse bootstrap process
- Module enablement and readiness checking
- NodeGroup and node management
- SSH connection and tunnel management

**Key Functions**:
```go
CreateTestCluster(ctx, configPath) (*TestClusterResources, error)
CleanupTestCluster(ctx, resources) error
```

### 3.3 Kubernetes Module (`pkg/kubernetes/` and `internal/kubernetes/`)

```
pkg/kubernetes/                    # Public Kubernetes utilities
├── apply.go                       # YAML manifest application (ApplyYAML, CreateYAML)
├── blockdevice.go                 # BlockDevice operations
├── client.go                      # Clientset/dynamic client with retry
├── localstorageclass.go           # LocalStorageClass CR operations
├── lvmvolumegroup.go              # LVMVolumeGroup operations
├── modules.go                     # Module configuration and readiness checking
├── namespace.go                   # Namespace utilities
├── nodegroup.go                   # NodeGroup operations
├── nodes.go                       # Node listing, taints, labels
├── pod.go                         # Pod operations (WaitForPodsStatus)
├── pvc.go                         # PVC operations (WaitForPVCsBound, WaitForPVCsResized, ResizeList)
├── secrets.go                     # Secret operations
├── storageclass.go                # StorageClass get/wait/default
├── virtualdisk.go                 # VirtualDisk attach/detach
└── vmpod.go                       # VM pod lookup

internal/kubernetes/               # Internal Kubernetes clients
├── commander/                     # Deckhouse Commander HTTP API client
│   ├── client.go                  # Commander client (clusters, templates, kubeconfig)
│   ├── errors.go                  # Error types
│   └── types.go                   # API DTOs
├── deckhouse/                     # Deckhouse-specific resources
│   ├── client.go                  # Deckhouse client (controller-runtime based)
│   ├── modules.go                 # Module operations (GetModule, CreateModuleConfig, etc.)
│   ├── nodegroups.go              # NodeGroup management
│   └── types.go                   # Deckhouse type definitions
├── storage/                       # SDS node-configurator CRDs
│   ├── blockdevice.go             # BlockDevice client
│   └── lvmvolumegroup.go          # LVMVolumeGroup client
└── virtualization/                # Virtualization resources
    ├── client.go                  # Virtualization client
    ├── virtual_machine.go         # VirtualMachine CRUD
    ├── virtual_disk.go            # VirtualDisk operations
    ├── virtual_image.go           # VirtualImage management
    ├── cluster_virtual_image.go   # ClusterVirtualImage ops
    └── vm_block_device.go         # VMBlockDevice operations
```

**Responsibilities**:
- Kubernetes API operations using standard `kubernetes.Clientset` and `dynamic.Interface`
- Resource-specific helper functions for common operations
- Status checking and waiting utilities
- YAML manifest application
- Module configuration with dependency handling
- Custom resource management (Deckhouse, virtualization)

**Key Features**:
- Uses standard Kubernetes client-go libraries (no custom wrappers)
- Helper functions in `pkg/kubernetes/` for common operations
- Module configuration with topological sort for dependencies
- Parallel module configuration and readiness checking
- Support for Custom Resources via dynamic client

### 3.4 Infrastructure Module (`internal/infrastructure/`)

```
infrastructure/ssh/
├── client.go           # SSH client implementation
├── interface.go        # SSH client interface
├── tunnel.go           # Port forwarding and tunneling
└── types.go            # SSH-related types
```

**Responsibilities**:
- SSH connection establishment and management
- SSH key handling
- Port forwarding (e.g., for Kubernetes API access)
- Remote command execution
- File transfer operations

**Key Features**:
- Support for password and key-based authentication
- SSH tunneling for accessing remote Kubernetes clusters
- Connection pooling and reuse
- Proper resource cleanup

### 3.5 Logger Module (`internal/logger/`)

```
logger/
├── logger.go           # Main logger implementation
├── handler.go          # Custom console handler with colors
├── level.go            # Log level parsing
├── config.go           # Logger configuration
├── multi_handler.go    # Multiple output support
└── README.md           # Logging documentation
```

**Responsibilities**:
- Structured logging with slog
- Colorized console output
- Optional JSON file logging
- Emoji indicators for different message types
- Configurable log levels

**Key Features**:
- DEBUG, INFO, WARN, ERROR levels
- Emoji prefixes for visual clarity (▶️ ✅ ⚠️ ❌ 🔧 ⏳)
- Dual output (console + file)
- Context-aware logging

### 3.6 Public API (`pkg/`)

```
pkg/
├── cluster/
│   ├── cluster.go      # Main cluster lifecycle (CreateTestCluster, CleanupTestCluster)
│   ├── setup.go        # Cluster setup and bootstrap operations
│   ├── lock.go         # Cluster locking (ConfigMap-based)
│   └── vms.go          # VM lifecycle management
├── kubernetes/
│   ├── apply.go        # YAML manifest application
│   ├── blockdevice.go  # BlockDevice operations
│   ├── cephblockpool.go         # Rook CephBlockPool CRUD + wait
│   ├── cephcluster.go           # Rook CephCluster CRUD + wait
│   ├── cephclusterconnection.go # csi-ceph CephClusterConnection/Auth CRs
│   ├── cephcredentials.go       # Read fsid/mons/admin-key from Rook secrets
│   ├── cephstorageclass.go      # csi-ceph CephStorageClass CR
│   ├── client.go       # Clientset/dynamic client with retry
│   ├── localstorageclass.go  # LocalStorageClass CR operations
│   ├── lvmvolumegroup.go     # LVMVolumeGroup operations
│   ├── modules.go      # Module configuration with dependency handling
│   ├── namespace.go    # Namespace utilities
│   ├── nodegroup.go    # NodeGroup operations
│   ├── nodes.go        # Node listing, taints, labels
│   ├── pod.go          # Pod operations
│   ├── pvc.go          # PVC operations
│   ├── rookconfigoverride.go    # Rook global ceph.conf override
│   ├── secrets.go      # Secret operations
│   ├── storageclass.go # StorageClass get/wait/default
│   ├── storageclass_manage.go   # Global default-SC management
│   ├── virtualdisk.go  # VirtualDisk attach/detach
│   ├── vmpod.go        # VM pod lookup
│   └── volumesnapshotclass.go   # VolumeSnapshotClass helpers
├── retry/
│   └── retry.go        # Generic retry with exponential backoff
└── testkit/
    ├── ceph.go          # EnsureCephStorageClass / EnsureDefaultCephStorageClass
    ├── ceph_cluster.go  # EnsureCephCluster (Rook-only, no csi-ceph)
    ├── ceph_crc.go      # Ceph CRC tuning helpers
    ├── storageclass.go  # EnsureDefaultStorageClass (sds-local-volume)
    └── stress-tests.go  # Stress test runner
```

**Responsibilities**:
- Public API for test implementations
- Cluster lifecycle management (create, wait, cleanup)
- Kubernetes resource utilities using standard clients
- Module configuration with automatic dependency resolution
- Test utilities and helpers
- Well-documented interfaces

---

## 4. Key Design Principles

### 4.1 Modular Package Structure

**Internal vs Public Packages**:
- `internal/` - Cannot be imported outside the module, allows for safe refactoring
- `pkg/` - Public API, stable interfaces for test implementations
- Clear separation between implementation details and public API

### 4.2 Configuration via Environment Variables

- All configuration through environment variables
- Clear validation with helpful error messages
- Sensible defaults for optional settings
- `test_exports` file pattern for easy configuration management

### 4.3 Automatic Cluster Lifecycle Management

- Tests focus on testing, not infrastructure
- Cluster creation, readiness checking, and cleanup automated
- Configurable cleanup behavior (`TEST_CLUSTER_CLEANUP`)
- Proper context handling for timeouts and cancellation

### 4.4 Ginkgo Test Framework

- Uses Ginkgo v2 for BDD-style testing
- Ordered test execution with proper dependency handling
- Clear lifecycle hooks (BeforeSuite, BeforeAll, AfterAll, AfterSuite)
- Descriptive test output with step-by-step progress

### 4.5 Comprehensive Logging

- Structured logging with slog
- Multiple log levels (DEBUG, INFO, WARN, ERROR)
- Visual indicators (emojis) for different message types
- Dual output (console + optional file logging)
- Configurable via `LOG_LEVEL` environment variable

### 4.6 Template-Based Test Creation

- Automated test creation via `create-test.sh` script
- Consistent test structure across all test suites
- Reduces boilerplate and setup time
- Easy onboarding for new tests

---

## 5. Benefits of Current Architecture

### 5.1 Maintainability
- **Clear Structure**: Easy to locate functionality by domain
- **Modular Design**: Each package has a single, well-defined responsibility
- **Documentation**: Comprehensive README files and inline documentation
- **Standardized**: All tests follow the same pattern

### 5.2 Developer Experience
- **Fast Onboarding**: Create new tests in minutes with `create-test.sh`
- **Clear Configuration**: Environment variables with validation and defaults
- **Rich Logging**: Visual progress indicators and detailed debug output
- **Helpful Errors**: Clear error messages guide troubleshooting

### 5.3 Test Quality
- **Consistent Structure**: All tests use the same lifecycle pattern
- **Automatic Cleanup**: Resources cleaned up regardless of test outcome
- **Proper Ordering**: Tests run in correct dependency order
- **Isolation**: Each test suite has its own cluster namespace

### 5.4 Extensibility
- **Modular Kubernetes Clients**: Easy to add new resource types
- **Configuration System**: Easy to add new configuration options
- **Template Pattern**: New tests inherit all framework improvements
- **Clean Interfaces**: Well-defined boundaries between components

### 5.5 Observability
- **Structured Logging**: Consistent log format across all operations
- **Progress Tracking**: Clear indication of long-running operations
- **Debug Mode**: Detailed information when troubleshooting
- **Step-by-Step Output**: Major operations clearly marked and tracked

---

## 6. Usage Examples

### 6.1 Creating a New Test

```bash
# Create a new test
cd tests/
./create-test.sh pvc-operations

# Configure environment
cd pvc-operations/
vi test_exports  # Edit with your credentials

# Run the test with new cluster creation
export TEST_CLUSTER_CREATE_MODE=alwaysCreateNew
source test_exports
go test -v -timeout=60m

# Or run using an existing cluster (faster, no VMs created)
export TEST_CLUSTER_CREATE_MODE=alwaysUseExisting
source test_exports
go test -v -timeout=60m
```

### 6.1.1 Using Existing Cluster Mode

When using `TEST_CLUSTER_CREATE_MODE=alwaysUseExisting`:

1. The test connects to the cluster specified by `SSH_HOST` and `SSH_USER`
2. A cluster lock (ConfigMap `e2e-cluster-lock` in `default` namespace) is acquired
3. If the cluster is already locked by another test, the test fails with lock info
4. After test completion, the lock is automatically released

```bash
# Check if a cluster is locked (from your machine)
kubectl get configmap e2e-cluster-lock -n default -o yaml

# Force release a stale lock (use with caution!)
kubectl delete configmap e2e-cluster-lock -n default
```

### 6.2 Test Implementation Example

```go
// pvc_operations_test.go
var _ = Describe("PVC Operations", Ordered, func() {
    var testClusterResources *cluster.TestClusterResources

    BeforeAll(func() {
        // Output environment configuration
        // ... (automatically generated)
    })

    AfterAll(func() {
        // Cleanup test cluster
        // ... (automatically generated)
    })

    It("should create test cluster", func() {
        ctx := context.Background()
        
        By("Creating test cluster", func() {
            var err error
            testClusterResources, err = cluster.CreateTestCluster(ctx, "cluster_config.yml")
            Expect(err).NotTo(HaveOccurred())
            // Note: CreateTestCluster automatically waits for modules to be ready
        })
    })

    It("should create and resize PVC", func() {
        // Your test logic here
        By("Creating PVC", func() {
            // ... test implementation
        })

        By("Resizing PVC", func() {
            // ... test implementation
        })
    })
})
```

### 6.3 Using the Logger

```go
import "github.com/deckhouse/storage-e2e/internal/logger"

// Major step
logger.Step(1, "Creating virtual machines")

// Step completion
logger.StepComplete(1, "Created %d VMs successfully", len(vms))

// Debug information (only shown at DEBUG level)
logger.Debug("VM details: %+v", vmDetails)

// Progress indicator (only shown at DEBUG level)
logger.Progress("Waiting for pods to become ready (%d/%d)", ready, total)

// Warnings
logger.Warn("Resource limit approaching: %d%%", percentage)

// Errors
logger.Error("Failed to create resource: %v", err)
```

---

## 7. Environment Variables Reference

### Required Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `TEST_CLUSTER_CREATE_MODE` | Cluster creation mode: `alwaysCreateNew` (creates VMs), `alwaysUseExisting` (uses existing cluster with lock), or `commander` (uses Deckhouse Commander) | `alwaysCreateNew` |
| `DKP_LICENSE_KEY` | Deckhouse license key | `X7Yig...` |
| `REGISTRY_DOCKER_CFG` | Docker registry credentials | `eyJhd...` |
| `SSH_USER` | SSH username for base/target cluster | `tfadm` |
| `SSH_HOST` | SSH host for base/target cluster | `172.17.1.67` |
| `TEST_CLUSTER_STORAGE_CLASS` | Storage class for test VMs (required for `alwaysCreateNew` mode) | `lsc-thick` |

### Optional Variables (with defaults)

| Variable | Default | Description |
|----------|---------|-------------|
| `YAML_CONFIG_FILENAME` | `cluster_config.yml` | Cluster configuration file |
| `SSH_PRIVATE_KEY` | `~/.ssh/id_rsa` | SSH private key path |
| `SSH_PUBLIC_KEY` | `~/.ssh/id_rsa.pub` | SSH public key path |
| `SSH_VM_USER` | `cloud` | SSH user for VMs |
| `TEST_CLUSTER_NAMESPACE` | `e2e-test-cluster` | Test namespace name |
| `TEST_CLUSTER_CLEANUP` | `false` | Cleanup cluster after tests |
| `LOG_LEVEL` | `debug` | Log level (debug/info/warn/error) |
| `KUBE_CONFIG_PATH` | - | Fallback kubeconfig path |

### Commander Variables (only when `TEST_CLUSTER_CREATE_MODE=commander`)

| Variable | Default | Description |
|----------|---------|-------------|
| `COMMANDER_URL` | (required) | URL of the Deckhouse Commander API |
| `COMMANDER_TOKEN` | (required) | API token for Commander authentication |
| `COMMANDER_CLUSTER_NAME` | `e2e-test-cluster` | Name of the cluster in Commander |
| `COMMANDER_TEMPLATE_NAME` | - | Template name for creating a new cluster |
| `COMMANDER_TEMPLATE_VERSION` | latest | Template version to use |
| `COMMANDER_CREATE_IF_NOT_EXISTS` | `false` | Create cluster if it doesn't exist |
| `COMMANDER_WAIT_TIMEOUT` | `30m` | Timeout for waiting for cluster readiness |
| `COMMANDER_AUTH_METHOD` | `x-auth-token` | Auth method (`x-auth-token`, `bearer`, `basic`, etc.) |
| `COMMANDER_API_PREFIX` | `/api/v1` | API path prefix |

---

## 8. Best Practices

### 8.1 Test Organization

- One test suite per major feature area
- Use `BeforeAll` for setup, `AfterAll` for cleanup
- First `It` block creates/initializes cluster
- Subsequent `It` blocks run actual tests
- Use `Ordered` container for dependent tests

### 8.2 Configuration Management

- Use `test_exports` file for environment variables
- Never commit credentials to git (already in `.gitignore`)
- Validate environment early (in `BeforeSuite`)
- Provide clear error messages for missing configuration

### 8.3 Resource Cleanup

- Set `TEST_CLUSTER_CLEANUP=true` for CI/CD
- Set `TEST_CLUSTER_CLEANUP=false` for debugging
- Bootstrap node always cleaned up
- Test VMs cleaned up only if cleanup enabled

### 8.4 Using Existing Cluster Mode

- Use `TEST_CLUSTER_CREATE_MODE=alwaysUseExisting` for faster test iterations
- The cluster lock prevents concurrent test execution on the same cluster
- If a test crashes, manually delete the lock ConfigMap: `kubectl delete configmap e2e-cluster-lock -n default`
- Existing cluster mode is ideal for debugging and iterative development
- New cluster mode (`alwaysCreateNew`) is recommended for CI/CD to ensure clean state

### 8.5 Logging

- Use `logger.Step()` for major operations
- Use `logger.Debug()` for detailed information
- Use `logger.Progress()` for wait operations
- Include relevant context in log messages

---

## 9. Future Improvements

### 9.1 Potential Enhancements

- [x] Support for existing cluster reuse (`alwaysUseExisting` mode)
- [x] Deckhouse Commander integration (`commander` mode)
- [ ] Parallel test execution support
- [ ] Test result reporting and metrics
- [ ] Integration with CI/CD systems
- [ ] Performance benchmarking framework
- [ ] More granular cleanup options
- [ ] Support for deploying different numbers of workers and masters with the same config

### 9.2 Technical Debt

- Some global state in configuration (being phased out)
- Limited unit test coverage (focus on E2E currently)

---

## Conclusion

The E2E test framework provides a robust, maintainable foundation for testing Deckhouse storage components. Key strengths include:

1. **Automated Lifecycle Management**: Complete cluster creation, ready-checking, and cleanup
2. **Developer-Friendly**: Quick test creation with templates and scripts
3. **Observable**: Rich logging with visual indicators
4. **Modular**: Clean package structure with clear responsibilities
5. **Configurable**: Environment-based configuration with validation

The framework enables rapid development of comprehensive E2E tests while maintaining code quality and developer productivity.

