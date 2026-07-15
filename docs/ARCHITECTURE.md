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
│   │   ├── clusterlock/          # Cluster lock used by pkg/e2e
│   │   │   └── lease.go          # coordination.k8s.io/v1 Lease lock with background renewal
│   │   ├── kubeaccess/           # Shared kubeconfig/rest.Config plumbing for providers
│   │   │   └── kubeaccess.go     # FetchKubeconfig, RewriteServer, BuildRestConfig(Direct), TunnelRestConfig, DirectReachable
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
│   │   └── ssh/                  # SSH operations (legacy)
│   │       ├── client.go
│   │       ├── interface.go
│   │       ├── tunnel.go
│   │       ├── types.go
│   │       └── v2/               # Self-healing SSH client (Dialer/Route + Tunnel)
│   │           ├── client.go     # New, Client, Close + package docs
│   │           ├── conn.go       # connection core: snapshot/refresh/keepalive + withConn
│   │           ├── dialer.go     # Dialer interface, Route, chain closer
│   │           ├── endpoint.go   # Endpoint, auth, host/key resolution
│   │           ├── errors.go     # transient classification
│   │           ├── options.go    # functional options
│   │           ├── retry.go      # NewWithRetry: retry initial dial until connect/timeout
│   │           └── tunnel.go     # Tunnel, accept loop
│   │
│   ├── provisioning/             # Cluster provisioning strategies (Provider impls)
│   │   ├── commander/           # Deckhouse Commander provider (ConnectTestCluster is a not-implemented stub — SDK support is a separate task)
│   │   │   ├── provider.go      # Commander-backed Provider (Bootstrap/Remove)
│   │   │   ├── connect.go       # connector: master from connection API, kubeconfig fetch + API tunnel via kubeaccess (legacy Connector)
│   │   │   ├── modules.go       # module enablement during Bootstrap
│   │   │   └── config.go        # Commander provider configuration (E2E_COMMANDER_*)
│   │   └── dvp/                 # DVP (Deckhouse Virtualization Platform) provider
│   │       ├── provider.go      # dvpProvider: Bootstrap (provision + installDeckhouse via cleanupStack) / Remove
│   │       ├── connect.go       # dvpConnector: direct-reachability probe or SSH tunnel to base cluster + per-VM executors (baseEndpoints/VMExecutor) + openTunnelToVM/connectToMaster
│   │       ├── connect_test_cluster.go # Provider.ConnectTestCluster: connect orchestration (base cluster → master → capabilities)
│   │       ├── vm_ip_resolver.go # vmIPResolver: node name → VM IP on the base cluster
│   │       ├── node_executor.go # dvpNodeExecutor: SSH command execution on test cluster nodes
│   │       ├── disks.go         # dvpDiskManager: DiskManager via VirtualDisk/VMBDA on the base cluster
│   │       ├── deps.go          # DI seam: baseConnector/masterConnector/kubeOps/fleetFactory + remoteExecutor + adapters
│   │       ├── setupnode.go     # setup-node synthesis (newSetupNode, fixed name) + readiness gating (buildDockerReadyCommand + waitDockerReady)
│   │       ├── bootstrap.go     # dhctl bootstrap-config rendering (param derivation + render + CIDR calc)
│   │       ├── bootstrap.tpl    # Embedded dhctl bootstrap config template
│   │       ├── dhctl.go         # dhctl bootstrap on setup node: pure builders (login/bootstrap cmd/connection-config/write-file) + orchestration
│   │       ├── nodes.go         # node join: buildNodeBootstrapCommand + isRetryableJoinError + joinNodes (errgroup + bounded retry)
│   │       ├── modules.go       # module enable: pure buildModuleLevels (topo sort) + moduleApplier seam + enableModulesInLevels (client-go, no SSH)
│   │       ├── install.go       # post-install k8s waits on rest.Config: waitBootstrapSecrets/waitNodesReady/checkHealth (client-go)
│   │       ├── config.go        # Config, Credentials, env parsing/validation
│   │       ├── kubeconfig.go    # ssh public-key derivation from a private key
│   │       └── vm/              # VM graph provisioning in the base cluster
│   │           ├── client.go    # Virtualization client wrapper
│   │           ├── build.go     # VM/disk/image resource builders
│   │           ├── create.go    # Resource creation
│   │           ├── provision.go # Provision/Teardown orchestration
│   │           ├── wait.go      # Readiness/deletion polling
│   │           ├── naming.go    # Resource naming
│   │           ├── labels.go    # Resource labels
│   │           └── cloudinit.go # cloud-init rendering
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
│   ├── cluster/                  # Legacy cluster management API (Deprecated: use pkg/e2e)
│   │   ├── cluster.go            # Main cluster creation/management
│   │   ├── setup.go              # Cluster setup and bootstrap operations
│   │   ├── lock.go               # Cluster locking (ConfigMap-based, deprecated)
│   │   └── vms.go                # VM lifecycle management
│   │
│   ├── clusterprovider/          # Provider contracts + env config
│   │   ├── provider.go           # Provider (Bootstrap/Remove/ConnectTestCluster) + legacy Connector
│   │   ├── cluster.go            # Cluster aggregate + ErrConnectUnsupported
│   │   ├── nodeexec.go           # NodeExecutor contract + ExecResult
│   │   ├── disks.go              # DiskManager contract + DiskSpec/Disk + ErrDisksUnsupported
│   │   ├── config.go             # ClusterConfig (E2E_TEST_CLUSTER_PROVIDER, E2E_CLUSTER_CONFIG_YAML_PATH)
│   │   ├── mode.go               # ProviderMode (dvp | commander)
│   │   └── registry/             # Provider mode → constructor registry
│   │
│   ├── e2e/                      # Test-suite SDK: attach to a provider-managed cluster
│   │   ├── e2e.go                # Connect (env → registry → ConnectTestCluster → health check → Lease lock) + options
│   │   ├── cluster.go            # Cluster handle: RESTConfig/Clientset/Dynamic + Nodes()/Disks() + Close
│   │   ├── health.go             # Post-connect cluster health check
│   │   └── conformance/          # Provider conformance checks (run against a live cluster)
│   │       └── conformance.go    # Verify / VerifyNodeExecutor / VerifyDiskManager
│   │
│   ├── kubernetes/               # Public Kubernetes utilities
│   │   ├── apply.go              # YAML manifest application
│   │   ├── blockdevice.go        # BlockDevice operations
│   │   ├── cephblockpool.go      # Rook CephBlockPool operations
│   │   ├── cephcluster.go        # Rook CephCluster operations
│   │   ├── cephfilesystem.go     # Rook CephFilesystem operations
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
│   │   ├── pod_exec.go           # Pods/exec helpers + DistrolessReader for distroless containers
│   │   ├── poll.go               # Generic readiness poller (per-call timeout, WARN on net errors)
│   │   ├── pvc.go                # PVC operations
│   │   ├── secrets.go            # Secret operations
│   │   ├── storageclass.go       # StorageClass get/wait/default
│   │   ├── virtclient.go         # Virtualization client constructor
│   │   ├── virtualdisk.go        # VirtualDisk attach/detach
│   │   └── vmpod.go              # VM pod lookup
│   │
│   ├── storage-e2e/              # Framework initialization helpers
│   │   └── setup.go              # Logger and environment initialization
│   │
│   ├── retry/                    # Generic retry with exponential backoff
│   │   └── retry.go
│   │
│   └── testkit/                  # Test framework utilities
│       ├── storageclass.go       # Default StorageClass provisioning
│       ├── stress-tests.go       # Stress test runner
│       ├── ceph.go               # EnsureCephStorageClass (Rook + csi-ceph)
│       └── ceph_cluster.go       # EnsureCephCluster (Rook only, no csi-ceph)
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
├── e2e/                           # Separate Go module: storage-e2e's own e2e suite
│   ├── go.mod                    # module github.com/deckhouse/storage-e2e/e2e
│   ├── e2e_suite_test.go         # Ginkgo runner (TestE2E)
│   ├── e2e_test.go               # Labeled specs: smoke/integration/regress/stress-test
│   └── cluster_config.yml        # Cluster definition for the self-test bootstrap
│
├── cmd/                           # Pipeline entrypoints
│   ├── bootstrap-cluster/        # `go run` target used by the CI bootstrap job
│   └── remove-cluster/           # `go run` target used by the CI teardown job
│
├── .github/                       # CI
│   ├── workflows/
│   │   ├── e2e.yml               # Reusable pipeline (resolve → bootstrap → run-tests → teardown)
│   │   ├── e2e-self-test.yml     # Caller running e2e.yml against storage-e2e itself
│   │   ├── go-checks.yml         # Lint + unit tests + coverage
│   │   └── gitleaks.yml          # Secret scanning
│   ├── scripts/
│   │   ├── e2e-resolve-labels.sh # PR labels → keep_cluster/ginkgo_filter/namespace
│   │   ├── e2e-prune-workspace.sh # Prune stale Go-cache trees (creds passed inline)
│   │   ├── e2e-run-tests.sh      # go mod replace + go test
│   │   └── tests/                # Bash tests for the scripts above
│   └── templates/
│       └── e2e-tests.yml         # Copy-ready caller for consumer modules
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
├── hack/
│   └── deckhouse-stub/           # Empty module; replace target for unpublished deckhouse submodules
│       └── go.mod
│
├── go.mod                         # replace block points unused deckhouse submodules at hack/deckhouse-stub
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
// Deprecated: superseded by the Lease-based lock acquired by e2e.Connect.
func AcquireClusterLock(ctx context.Context, kubeconfig *rest.Config, testName string) error
func ReleaseClusterLock(ctx context.Context, kubeconfig *rest.Config) error
func IsClusterLocked(ctx context.Context, kubeconfig *rest.Config) (bool, error)
func GetClusterLockInfo(ctx context.Context, kubeconfig *rest.Config) (*ClusterLockInfo, error)
```

#### Cluster Locking Mechanism

> **Deprecated.** The ConfigMap lock below is the legacy `pkg/cluster` mechanism.
> The current mechanism is a `coordination.k8s.io/v1` **Lease** named
> `e2e-cluster-lock` in the `default` namespace, acquired by `e2e.Connect` and
> renewed in the background (default duration 5m); a lease left behind by a
> dead run self-expires and is taken over by the next run. Holder metadata
> (test name, user, hostname, pid) is stored in the Lease annotations.

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
├── overrides.go        # Per-module modulePullOverride env overrides
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
├── lock.go             # Cluster locking (ConfigMap-based, deprecated — see Lease lock in internal/kubernetes/clusterlock)
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
├── virtclient.go                  # Virtualization client constructor
├── virtualdisk.go                 # VirtualDisk attach/detach
└── vmpod.go                       # VM pod lookup

internal/kubernetes/               # Internal Kubernetes clients
├── clusterlock/                   # Lease-based cluster lock used by pkg/e2e
│   └── lease.go                   # AcquireLease/Release with background renewal
├── kubeaccess/                    # Shared kubeconfig/rest.Config plumbing for providers
│   └── kubeaccess.go              # FetchKubeconfig, RewriteServer, BuildRestConfig(Direct), TunnelRestConfig, DirectReachable
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
├── client.go           # SSH client implementation (Exec, ExecCapture, tunnels) [legacy]
├── interface.go        # SSH client interface [legacy]
├── tunnel.go           # Port forwarding and tunneling [legacy]
├── types.go            # SSH-related types [legacy]
└── v2/                 # Self-healing SSH client (see below)
    ├── client.go       # New, Client, Close + package docs
    ├── conn.go         # connection core: snapshot/refresh/keepalive + withConn executor
    ├── dialer.go       # Dialer interface, Route, chain closer
    ├── endpoint.go     # Endpoint, auth, host/key resolution
    ├── errors.go       # transient classification
    ├── options.go      # functional options
    ├── retry.go        # NewWithRetry: retry initial dial until connect/timeout
    └── tunnel.go       # Tunnel, accept loop
```

**Responsibilities**:
- SSH connection establishment and management
- SSH key handling
- Port forwarding (e.g., for Kubernetes API access)
- Remote command execution
- Remote command execution with separated stdout/stderr capture for diagnostics
- File transfer operations (including UploadPrivate: chmod-before-data for sensitive payloads)

**Key Features**:
- Support for password and key-based authentication
- SSH tunneling for accessing remote Kubernetes clusters
- Connection pooling and reuse
- `ExecCapture` keeps stdout and stderr separate while preserving retry/reconnect behavior
- Proper resource cleanup

#### 3.4.1 Self-healing SSH client (`internal/infrastructure/ssh/v2/`)

A ground-up rewrite that lives in parallel with the legacy package (no consumers
migrated yet). It separates **how we connect** (directly or via jump hosts) from
**what we do over the connection** (tunneling and remote command execution), and
hides every reconnect from callers.

**Design**:

- `Dialer` is the injection point: `Dial(ctx) (*ssh.Client, io.Closer, error)` +
  `Describe()`. `Route(first Endpoint, more ...Endpoint)` builds the built-in
  implementation; the last hop is always the target, so the `(first, more...)`
  signature guarantees at least one hop at compile time. The returned `io.Closer`
  tears down the whole chain (target + every jump + ssh-agent connections).
- `Endpoint` describes a single host: `User`, `Addr` (`host` or `host:port`,
  default `:22`), `KeyData` (raw private-key bytes; the transport layer never
  reads files or expands paths — callers resolve key bytes themselves), optional
  `Passphrase` (falls back to ssh-agent), optional per-hop `HostKey`.
- The unexported `conn` core owns the current `*ssh.Client`, its chain `Closer`,
  and a generation counter under a mutex. `snapshot` reads them; `refresh`
  re-dials via `singleflight` keyed on the failed generation so concurrent
  reconnects collapse into one and a stale generation never tears down a freshly
  healed link. The slow `Dial` runs outside the lock on a connection-lifetime
  context (`lifeCtx`, derived from the constructor ctx via `context.WithoutCancel`
  and cancelled by `Close`) + timeout: one caller's cancellation can't abort the
  shared flight, yet `Close` aborts an in-flight reconnect immediately.
- A single generic executor `withConn[T]` runs an operation against the live
  client and heals on transient failures (bounded by `WithRetries`); the tunnel
  and `Exec` use it today and `Upload` is designed to reuse it unchanged.
- `Exec` (`exec.go`) follows the only-open-retry contract: just the
  `client.NewSession()` step runs through `withConn`, so a transient open failure
  heals and reopens before any command has run. The command itself (`Start` +
  `Wait`, in `runWithContext`) then runs **exactly once** outside the healing
  loop — a mid-flight drop surfaces the error instead of re-running the command,
  to avoid duplicate side effects. Context cancellation signals `SIGKILL`, closes
  the session, and returns `ctx.Err()`.
- Optional keepalive (`WithKeepalive`) probes the link and heals through the same
  `refresh` path; every heal is logged at WARN. The probe reply timeout is
  independent of the probe interval (`WithKeepaliveTimeout`, default
  `min(interval, 10s)`).

**Public API v1**: `New(ctx, Dialer, ...Option)`, `Client.Tunnel(ctx, remotePort)`
(self-healing local forward on a free `127.0.0.1` port; `Tunnel.LocalAddr`,
`Tunnel.Close`), `Client.Exec(ctx, cmd)` (returns `ExecResult{Stdout, Stderr,
ExitCode}`; `ExitCode` is meaningful only when `err` is nil or a non-zero command
exit — on transport/cancel errors it stays `0` and the caller must inspect `err`),
`Client.Close`. Options: `WithKeepalive`, `WithKeepaliveTimeout`,
`WithRetries`, `WithLogger`, `WithHostKeyCallback`, `WithInsecureIgnoreHostKey`
(host key defaults to `InsecureIgnoreHostKey` — a conscious default for ephemeral
e2e VMs; `New` logs a WARN whenever this insecure default is active). The host key
default is injected only into `Route`-built dialers; a custom `Dialer` handles its
own host key verification.

**Extension points (designed, not yet implemented)**: `Upload`. Transient-error
classification uses `errors.Is`/`errors.As` against standard types — never
error-string matching.

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

### 3.6 Storage E2E Module (`pkg/storage-e2e/`)

```
storage-e2e/
└── setup.go            # Framework initialization helpers
```

**Responsibilities**:
- Initializes common prerequisites for test runs
- Ensures logger is initialized before test actions
- Validates required environment configuration before cluster operations

**Key Functions**:
```go
Initialize() error
```

### 3.7 Public API (`pkg/`)

```
pkg/
├── cluster/            # Deprecated: legacy lifecycle API, use pkg/e2e for new suites
│   ├── cluster.go      # Main cluster lifecycle (CreateTestCluster, CleanupTestCluster)
│   ├── setup.go        # Cluster setup and bootstrap operations
│   ├── lock.go         # Cluster locking (ConfigMap-based, deprecated)
│   └── vms.go          # VM lifecycle management
├── clusterprovider/
│   ├── provider.go     # Provider (Bootstrap/Remove/ConnectTestCluster) + legacy Connector
│   ├── cluster.go      # Cluster aggregate + ErrConnectUnsupported
│   ├── nodeexec.go     # NodeExecutor contract + ExecResult
│   ├── disks.go        # DiskManager contract + DiskSpec/Disk + ErrDisksUnsupported
│   ├── config.go       # ClusterConfig from env (E2E_TEST_CLUSTER_PROVIDER, E2E_CLUSTER_CONFIG_YAML_PATH)
│   ├── mode.go         # ProviderMode (dvp | commander)
│   └── registry/       # Provider registry (mode → constructor)
├── e2e/
│   ├── e2e.go          # Connect(ctx, opts...) — SDK entry point for suites
│   ├── cluster.go      # Cluster handle (RESTConfig/Clientset/Dynamic/Nodes/Disks/Close)
│   ├── health.go       # Post-connect health check
│   └── conformance/    # Provider conformance checks (Verify*)
├── kubernetes/
│   ├── apply.go                 # YAML manifest application
│   ├── blockdevice.go           # BlockDevice operations
│   ├── cephblockpool.go         # Rook CephBlockPool CRUD + wait
│   ├── cephcluster.go           # Rook CephCluster CRUD + wait
│   ├── cephfilesystem.go        # Rook CephFilesystem CRUD + wait
│   ├── cephclusterconnection.go # csi-ceph CephClusterConnection/Auth CRs
│   ├── cephcredentials.go       # Read fsid/mons/admin-key from Rook secrets
│   ├── cephstorageclass.go      # csi-ceph CephStorageClass CR
│   ├── client.go                # Clientset/dynamic client with retry
│   ├── localstorageclass.go     # LocalStorageClass CR operations
│   ├── lvmvolumegroup.go        # LVMVolumeGroup operations
│   ├── modules.go               # Module configuration with dependency handling
│   ├── namespace.go             # Namespace utilities
│   ├── nodegroup.go             # NodeGroup operations
│   ├── nodes.go                 # Node listing, taints, labels
│   ├── pod.go                   # Pod operations
│   ├── pod_exec.go              # Exec helpers + DistrolessReader (ephemeral-container session)
│   ├── poll.go                  # pollResourceUntilReady helper for Wait*Ready callers
│   ├── pvc.go                   # PVC operations
│   ├── rookconfigoverride.go    # Rook global ceph.conf override
│   ├── secrets.go               # Secret operations
│   ├── storageclass.go          # StorageClass get/wait/create/default
│   ├── virtclient.go            # Virtualization client constructor
│   ├── virtualdisk.go           # VirtualDisk attach/detach
│   ├── vmpod.go                 # VM pod lookup
│   └── volumesnapshotclass.go   # VolumeSnapshotClass helpers
├── retry/
│   └── retry.go                 # Generic retry with exponential backoff
├── storage-e2e/
│   └── setup.go                 # Framework initialization (logger + env validation)
└── testkit/
    ├── storageclass.go          # Default StorageClass provisioning
    ├── stress-tests.go          # Stress test runner
    ├── ceph.go                  # EnsureCephStorageClass / EnsureDefaultCephStorageClass
    └── ceph_cluster.go          # EnsureCephCluster (Rook-only, no csi-ceph)
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
| `TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME` | `generic` | VM class for VMs on the base cluster in `alwaysCreateNew`. If set to another name (DNS-1123 subdomain) and the class does not exist, it is created from `generic` with `spec.cpu.type: Host`, **`spec.nodeSelector` / `spec.tolerations` cleared**, sizing policies retained from template, labeled `storage-e2e.deckhouse.io/auto-created=true`, and left after cleanup |
| `TEST_CLUSTER_CLEANUP` | `false` | Cleanup cluster after tests |
| `LOG_LEVEL` | `debug` | Log level (debug/info/warn/error) |
| `KUBE_CONFIG_PATH` | - | Explicit kubeconfig path. Used when SSH retrieval of `/etc/kubernetes/{super-admin,admin}.conf` from the master fails. If unset and SSH also fails, `GetKubeconfig` returns an error (no silent fallback to `~/.kube/config`). |
| `<MODULE>_MODULE_PULL_OVERRIDE` | - | Per-module override of a module's `modulePullOverride` at config load (module name upper-cased, non-`[A-Z0-9]` → `_`; e.g. `SDS_ELASTIC_MODULE_PULL_OVERRIDE`, `CSI_CEPH_MODULE_PULL_OVERRIDE`). Replaces the static `cluster_config.yml` tag for CI image builds (`pr<N>`/`mr<N>`); each applied override is logged at INFO. The static YAML stays literal — `${VAR}` inside `modulePullOverride` is still rejected. See `internal/config/overrides.go`. |
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

### Provider / SDK Variables (`pkg/e2e`, `cmd/bootstrap-cluster`, `cmd/remove-cluster`)

| Variable                       | Default    | Description                                   |
|--------------------------------|------------|-----------------------------------------------|
| `E2E_TEST_CLUSTER_PROVIDER`    | (required) | Cluster provider mode: `dvp` or `commander`   |
| `E2E_CLUSTER_CONFIG_YAML_PATH` | (required) | Path to the cluster bootstrap definition YAML |

The provider-specific `E2E_COMMANDER_*` / `E2E_DVP_*` variables (see the DVP
table above and `internal/provisioning/commander/config.go`) configure the
selected provider for all three phases: bootstrap, test run (`e2e.Connect`)
and teardown. `e2e.Connect` currently supports the `dvp` provider only;
commander's `ConnectTestCluster` is a not-implemented stub (separate task) and
`Connect` on it fails with `ErrConnectUnsupported`.

For the DVP base cluster, the connector first probes whether the configured
kubeconfig reaches the API server directly (`kubeaccess.DirectReachable`,
short `/version` probe); only when it does not is the SSH tunnel opened. No
extra configuration is needed — the detection is automatic.

### DVP Base Cluster Variables (mode `dvp`)

Each file-backed secret accepts **either a path or inline content** (exactly one;
enforced by `dvp.Config.Validate`). Locally pass a path; in CI pass the Secret
content directly. Inline content is less safe than a path (it sits in
`/proc/<pid>/environ`) — prefer a path on `tmpfs` when possible.

| Variable                                         | Default                  | Description                                         |
|--------------------------------------------------|--------------------------|-----------------------------------------------------|
| `E2E_DVP_BASE_CLUSTER_SSH_USER`                  | (required)               | SSH user for the base cluster                       |
| `E2E_DVP_BASE_CLUSTER_SSH_HOST`                  | (required)               | SSH host for the base cluster                       |
| `E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE`            | -                        | Passphrase for the SSH private key                  |
| `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY_PATH`      | (one of)                 | SSH private key path (`~`/`$ENV` expanded)          |
| `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY`           | (one of)                 | SSH private key inline content                      |
| `E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH`           | (one of)                 | Kubeconfig path (`~`/`$ENV` expanded)               |
| `E2E_DVP_BASE_CLUSTER_KUBECONFIG`                | (one of)                 | Kubeconfig inline content                           |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST`             | -                        | Jump host (all jump fields required together)       |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_USER`             | -                        | Jump host SSH user                                  |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_KEY_PASSPHRASE`   | -                        | Passphrase for the jump private key                 |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY_PATH` | (one of, with jump host) | Jump private key path                               |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_PRIVATE_KEY`      | (one of, with jump host) | Jump private key inline content                     |
| `E2E_DVP_BASE_CLUSTER_NAMESPACE`                 | `e2e-test-cluster`       | Test namespace name                                 |
| `E2E_DVP_VM_SSH_USER`                            | `cloud`                  | Login for provisioned VMs (setup/masters/workers)   |
| `E2E_DVP_DKP_LICENSE_KEY`                        | (required for Bootstrap) | DKP registry license token for dhctl install image  |
| `E2E_DVP_REGISTRY_DOCKER_CFG`                    | (required for Bootstrap) | base64 dockercfg embedded into the bootstrap config |

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
- `VirtualMachineClass` resources auto-created by the framework (custom class name with clone-from-generic logic) are **not** removed during cleanup; they remain cluster-scoped on the base cluster for idempotent re-runs

### 8.4 Using Existing Cluster Mode

- Use `TEST_CLUSTER_CREATE_MODE=alwaysUseExisting` for faster test iterations
- The cluster lock prevents concurrent test execution on the same cluster
- With `e2e.Connect` the lock is a Lease that self-expires after a crash (no manual cleanup needed); inspect it with
  `kubectl get lease e2e-cluster-lock -n default -o yaml`
- If a legacy (`pkg/cluster`) test crashes, manually delete the lock ConfigMap:
  `kubectl delete configmap e2e-cluster-lock -n default`
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
- [x] Provider SDK (`pkg/e2e`): unified `Cluster` handle with capability strategies (`NodeExecutor`,
  `DiskManager`) supplied by the provider; legacy `pkg/cluster` deprecated
- [ ] `DiskManager` for the Commander provider (template change + convergence); DVP-only for now,
  Commander gets the `ErrDisksUnsupported` stub
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

