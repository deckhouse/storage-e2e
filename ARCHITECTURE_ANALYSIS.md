# Architecture Analysis and Refactoring Plan

## Executive Summary

This document provides a deep analysis of the current `testkit_v2` codebase structure and proposes a clear, modular architecture to replace the current "pasta code" implementation. The analysis covers:

1. Current structure and dependencies
2. Architectural problems identified
3. Proposed target architecture
4. Detailed refactoring plan with step-by-step migration strategy

---

## 1. Current Structure Analysis

### 1.1 Package Structure

**Critical Finding**: All code is currently in a single package `integration`:
- `testkit_v2/tests/*` - All test files
- `testkit_v2/util/*` - All utility files

This monolith package design causes:
- No encapsulation boundaries
- Global state scattered across files
- Hidden circular dependencies
- Difficulty in testing components in isolation
- Hard to understand code flow and dependencies

### 1.2 File Organization

#### Test Files (`testkit_v2/tests/`)
```
tests/
├── 00_healthcheck_test.go          # Basic cluster health checks
├── 01_sds_nc_test.go               # LVG (LVM Volume Group) operations
├── 03_sds_lv_test.go               # PVC (Persistent Volume Claim) operations
├── 05_sds_node_configurator_test.go # Comprehensive LVM tests (thick/thin)
├── 99_finalizer_test.go            # Cleanup tests
├── tools.go                         # Shared test utilities
└── data-exporter/
    └── base_test.go                # Base test for data exporter feature
```

#### Utility Files (`testkit_v2/util/`)
```
util/
├── env.go                          # Environment config, flags, cluster types
├── filter.go                       # Filter/Where interfaces
├── kube_cluster_definitions.go    # Cluster definition types (NEW)
├── kube_cluster.go                # Cluster singleton/cache
├── kube_deckhouse_modules.go      # Deckhouse module management
├── kube_deploy.go                 # Deployment/Service operations
├── kube_modules.go                # Custom CRDs (SSHCredentials, StaticInstance)
├── kube_node.go                   # Node operations, execution
├── kube_secret.go                 # SSH credentials CRUD
├── kube_storage.go                # Storage (BD, LVG, PVC, SC)
├── kube_tester.go                 # Test execution helpers
├── kube_vm_cluster.go             # VM cluster creation, Deckhouse install
├── kube_vm.go                     # VM, VD, VMBD operations
├── kube.go                        # Core Kubernetes client setup
├── log.go                         # Logging utilities
├── ssh.go                         # SSH client operations
└── tools.go                       # Utility functions (retry, random)
```

### 1.3 Dependency Graph

```
Tests (integration package)
  └─> util package (imported as "github.com/deckhouse/sds-e2e/util")
        └─> Actually same package! Only different directory structure

Current Dependencies:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
kube_cluster.go (singleton/cache)
  ├─> env.go (envInit, global vars)
  ├─> kube.go (InitKCluster)
  └─> ssh.go (GetSshClient, tunnel creation)

kube.go (core client setup)
  ├─> kube_modules.go (D8SchemeBuilder)
  └─> Multiple Kubernetes API imports

kube_storage.go (storage operations)
  ├─> kube.go (KCluster type)
  ├─> filter.go (filters)
  └─> tools.go (RetrySec)

kube_node.go (node operations)
  ├─> kube.go (KCluster type)
  ├─> kube_modules.go (StaticInstance CRD)
  ├─> filter.go (filters)
  └─> ssh.go (ExecNodeSsh)

kube_vm_cluster.go (cluster creation)
  ├─> env.go (global vars)
  ├─> kube.go (InitKCluster)
  ├─> kube_vm.go (VM operations)
  ├─> kube_node.go (AddStaticNodes)
  ├─> ssh.go (SSH operations)
  └─> tools.go (retry utilities)

kube_vm.go (VM operations)
  ├─> kube.go (KCluster type)
  ├─> filter.go (filters)
  └─> tools.go (hashMd5)

All files → env.go (global state!)
All files → log.go (logging)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### 1.4 Major Architectural Problems

#### Problem 1: Global State Everywhere
- `env.go` contains ~50 global variables
- Package-level variables in multiple files (`clrCache`, `mx`, etc.)
- No dependency injection
- Hard to test in isolation
- Race conditions possible

#### Problem 2: God Object (`KCluster`)
- `KCluster` struct has 60+ methods
- Violates Single Responsibility Principle
- Methods span multiple domains:
  - Kubernetes API operations
  - Node management
  - Storage operations
  - VM operations
  - Module management
  - Deployment management

#### Problem 3: Mixed Concerns
- Business logic mixed with infrastructure
- Test utilities mixed with production code
- Configuration mixed with execution
- No clear separation of layers

#### Problem 4: Poor Encapsulation
- Everything in one package = no private boundaries
- Internal implementation details exposed
- Can't hide complexity behind interfaces

#### Problem 5: Circular Dependencies (Hidden)
- Files import each other indirectly
- Hidden cycles through globals
- `env.go` → everything, everything → `env.go`

#### Problem 6: Testing Difficulties
- Can't mock dependencies (globals)
- Hard to create isolated test scenarios
- Test files use same package = can access internals incorrectly

---

## 2. Target Architecture

### 2.1 Package Structure

```
testkit_v2/
├── cmd/
│   └── runner/                    # Test runner CLI (optional, for future)
│
├── internal/                      # Internal packages (not importable)
│   ├── config/                    # Configuration management
│   │   ├── env.go                # Environment variables
│   │   ├── flags.go              # CLI flags
│   │   ├── cluster_types.go      # Cluster type definitions
│   │   └── images.go             # OS image definitions
│   │
│   ├── cluster/                   # Cluster management
│   │   ├── manager.go            # Cluster manager (singleton replacement)
│   │   ├── client.go             # Kubernetes client factory
│   │   └── types.go              # Cluster types
│   │
│   ├── kubernetes/                # Kubernetes API operations
│   │   ├── core/                 # Core K8s resources
│   │   │   ├── namespace.go
│   │   │   ├── pod.go
│   │   │   ├── node.go
│   │   │   └── service.go
│   │   ├── apps/                 # Apps resources
│   │   │   ├── deployment.go
│   │   │   └── daemonset.go
│   │   ├── storage/              # Storage resources
│   │   │   ├── pvc.go
│   │   │   ├── storageclass.go
│   │   │   ├── blockdevice.go
│   │   │   └── lvmvolumegroup.go
│   │   ├── virtualization/       # VM resources
│   │   │   ├── vm.go
│   │   │   ├── vdisk.go
│   │   │   └── vmbd.go
│   │   └── deckhouse/            # Deckhouse resources
│   │       ├── modules.go
│   │       ├── nodegroups.go
│   │       └── staticinstance.go
│   │
│   ├── infrastructure/            # Infrastructure operations
│   │   ├── ssh/                  # SSH operations
│   │   │   ├── client.go
│   │   │   ├── keys.go
│   │   │   └── tunnel.go
│   │   └── vm/                   # VM provisioning
│   │       ├── provider.go       # Interface
│   │       └── deckhouse/        # Deckhouse VM provider
│   │           └── provider.go
│   │
│   ├── test/                     # Test framework utilities
│   │   ├── framework.go          # Test framework
│   │   ├── filters.go            # Filter implementations
│   │   ├── runner.go             # Test runner
│   │   └── node_test_context.go  # Node test context
│   │
│   └── utils/                    # Pure utility functions
│       ├── retry.go
│       ├── random.go
│       └── crypto.go
│
├── pkg/                           # Public API (importable)
│   ├── cluster/                  # Public cluster interface
│   │   ├── interface.go          # Cluster interface
│   │   └── config.go             # Cluster config types
│   │
│   └── testkit/                  # Testkit public API
│       ├── test.go               # Test helpers
│       └── fixtures.go           # Test fixtures
│
├── tests/                         # Test files
│   ├── healthcheck/
│   │   └── healthcheck_test.go
│   ├── storage/
│   │   ├── lvg_test.go
│   │   ├── pvc_test.go
│   │   └── lvm_test.go
│   ├── node_configurator/
│   │   └── node_configurator_test.go
│   ├── data_exporter/
│   │   └── data_exporter_test.go
│   └── cleanup/
│       └── finalizer_test.go
│
├── go.mod
└── README.md
```

### 2.2 Layer Architecture

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

### 2.3 Core Interfaces

#### Cluster Interface
```go
// pkg/cluster/interface.go
type Cluster interface {
    // Core operations
    Name() string
    Context() context.Context
    
    // Resource operations
    Namespaces() NamespaceClient
    Nodes() NodeClient
    Pods() PodClient
    Storage() StorageClient
    Virtualization() VirtualizationClient // TODO asergunov: Is the VirtualizationClient this one? https://github.com/deckhouse/virtualization/blob/main/api/client/kubeclient/client.go#L53
    Deckhouse() DeckhouseClient
    
    // Lifecycle
    EnsureReady(ctx context.Context) error
    Close() error
}
```

#### Resource Clients
```go
// Internal interfaces for resource operations
type StorageClient interface {
    BlockDevices() BlockDeviceClient
    LVMVolumeGroups() LVMVolumeGroupClient
    PersistentVolumeClaims() PersistentVolumeClaimClient
    StorageClasses() StorageClassClient
}

type NodeClient interface {
    List(ctx context.Context, filters ...NodeFilter) ([]Node, error)
    Get(ctx context.Context, name string) (*Node, error)
    Execute(ctx context.Context, name string, cmd []string) (stdout, stderr string, err error)
    // ...
}

type VirtualizationClient interface {
    VMs() VMClient
    VirtualDisks() VirtualDiskClient
    VirtualMachineBlockDevices() VMBDClient
}
```

### 2.4 Dependency Injection

**Cluster Manager Pattern**:
```go
// internal/cluster/manager.go
type Manager struct {
    config     *config.Config
    clusters   map[string]Cluster
    mu         sync.RWMutex
    logger     logger.Logger
    sshFactory ssh.Factory
}

func NewManager(cfg *config.Config, opts ...Option) *Manager {
    // Constructor with options for dependency injection
}

func (m *Manager) GetOrCreate(ctx context.Context, configPath, name string) (Cluster, error) {
    // Lazy initialization with proper error handling
}
```

### 2.5 Configuration Management

**Structured Configuration**:
```go
// internal/config/config.go
type Config struct {
    // Environment
    TestNS           string
    TestNSCleanUp    string
    KeepState        bool
    
    // Cluster configuration
    NestedCluster    NestedClusterConfig
    Hypervisor       HypervisorConfig
    
    // Feature flags
    SkipOptional     bool
    Parallel         bool
    TreeMode         bool
    
    // Logging
    Verbose          bool
    Debug            bool
    LogFile          string
}

type NestedClusterConfig struct {
    KubeConfig     string
    Host           string
    SSHUser        string
    SSHKey         string
    K8sPort        string
    StorageClass   string
}
```

---

## 3. Refactoring Plan

### 3.1 Phase 1: Foundation (Low Risk)

**Goal**: Extract configuration and utilities without breaking existing code.

#### Step 1.1: Extract Configuration
- [ ] Create `internal/config/` package
- [ ] Move `env.go` → `internal/config/env.go`
- [ ] Move cluster types → `internal/config/cluster_types.go`
- [ ] Move image definitions → `internal/config/images.go`
- [ ] Create `Config` struct to hold all configuration
- [ ] Create constructor `config.Load()` to initialize from flags/env
- [ ] Keep global variables temporarily with deprecation comments

**Migration Strategy**:
```go
// Keep existing globals for backward compatibility
var TestNS = config.Current().TestNS

// But internally use structured config
func EnsureCluster(...) {
    cfg := config.Current()
    // Use cfg instead of globals
}
```

#### Step 1.2: Extract Pure Utilities
- [ ] Create `internal/utils/` package
- [ ] Move `tools.go` utilities → `internal/utils/`
- [ ] Move `log.go` → `internal/logger/` with interface
- [ ] Create logger interface for testability
- [ ] Update all files to use logger interface

**Files Affected**:
- `util/tools.go` → `internal/utils/retry.go`, `random.go`, `crypto.go`
- `util/log.go` → `internal/logger/logger.go`

#### Step 1.3: Extract Filters
- [ ] Create `internal/test/filters.go`
- [ ] Move `filter.go` → `internal/test/filters.go`
- [ ] Make filters type-safe and well-documented
- [ ] Keep old imports working temporarily

**Estimated Time**: 1-2 days
**Risk Level**: Low (internal changes, maintain compatibility)

---

### 3.2 Phase 2: Extract Kubernetes Clients (Medium Risk)

**Goal**: Separate Kubernetes API operations from business logic.

#### Step 2.1: Create Kubernetes Client Packages
- [ ] Create `internal/kubernetes/` structure
- [ ] Extract core operations:
  - `kube.go` → `internal/kubernetes/client.go` (client factory)
  - `kube.go` (namespace) → `internal/kubernetes/core/namespace.go`
  - `kube_node.go` → `internal/kubernetes/core/node.go`
  - `kube_deploy.go` → `internal/kubernetes/apps/deployment.go`
- [ ] Extract storage operations:
  - `kube_storage.go` → `internal/kubernetes/storage/*.go`
  - Split into separate files per resource type
- [ ] Extract virtualization operations:
  - `kube_vm.go` → `internal/kubernetes/virtualization/*.go`

#### Step 2.2: Create Client Interfaces
- [ ] Define interfaces for each resource client
- [ ] Implement interfaces with existing code
- [ ] Update `KCluster` to use clients via composition

**Before**:
```go
type KCluster struct {
    controllerRuntimeClient ctrlrtclient.Client
    goClient                *kubernetes.Clientset
    // ... 60+ methods directly on KCluster
}
```

**After**:
```go
type KCluster struct {
    client kubernetes.Client
    storage *storage.Client
    nodes   *node.Client
    // ... composition instead of methods
}

type Client struct {
    controller ctrlrtclient.Client
    goClient   *kubernetes.Clientset
    // Resource clients
    namespaces  NamespaceClient
    nodes       NodeClient
    pods        PodClient
    storage     StorageClient
    // ...
}
```

#### Step 2.3: Update Tests Gradually
- [ ] Create wrapper functions in old package that delegate to new structure
- [ ] Update tests one by one to use new interfaces
- [ ] Remove old methods once all tests migrated

**Migration Helper**:
```go
// In old package (temporary compatibility layer)
func (cluster *KCluster) CreateLVG(...) error { // TODO: asergunov: Maybe D8Cluster? Or Cluster interface and d8.Cluster as its implementation
    return cluster.storage.LVMVolumeGroups().Create(...)
}
```

**Estimated Time**: 3-5 days
**Risk Level**: Medium (interface changes, needs careful testing)

---

### 3.3 Phase 3: Extract Infrastructure (Medium Risk)

**Goal**: Separate infrastructure concerns (SSH, VM provisioning).

#### Step 3.1: Extract SSH Operations
- [ ] Create `internal/infrastructure/ssh/` package
- [ ] Move `ssh.go` → `internal/infrastructure/ssh/`
- [ ] Create SSH client factory interface
- [ ] Make SSH client mockable for tests
- [ ] Update all SSH usages to use factory

#### Step 3.2: Extract VM Cluster Operations
- [ ] Create `internal/infrastructure/vm/` package
- [ ] Extract Deckhouse VM provider
- [ ] Move `kube_vm_cluster.go` logic → `internal/infrastructure/vm/deckhouse/`
- [ ] Create VM provider interface for extensibility
- [ ] Separate VM lifecycle from cluster operations

**Structure**:
```go
// internal/infrastructure/vm/provider.go
type Provider interface {
    CreateVM(ctx context.Context, spec VMSpec) (*VM, error)
    DeleteVM(ctx context.Context, name string) error
    WaitForVMReady(ctx context.Context, name string) error
}

// internal/infrastructure/vm/deckhouse/provider.go
type DeckhouseProvider struct {
    cluster Cluster
    // ...
}
```

#### Step 3.3: Extract Cluster Creation Logic
- [ ] Move cluster creation from `kube_vm_cluster.go`
- [ ] Create `internal/cluster/builder.go` for cluster creation
- [ ] Separate concerns: VM creation, Deckhouse installation, node registration

**Estimated Time**: 3-4 days
**Risk Level**: Medium (infrastructure changes affect tests)

---

### 3.4 Phase 4: Refactor Cluster Management (High Risk)

**Goal**: Replace singleton pattern with proper dependency injection.

#### Step 4.1: Create Cluster Manager
- [ ] Create `internal/cluster/manager.go`
- [ ] Replace `EnsureCluster` singleton with Manager
- [ ] Implement proper lifecycle management
- [ ] Add context support for cancellation

#### Step 4.2: Refactor KCluster to Cluster Interface
- [ ] Create `pkg/cluster/interface.go` with public Cluster interface
- [ ] Implement interface in `internal/cluster/cluster.go`
- [ ] Break up `KCluster` into smaller, focused structs
- [ ] Use composition instead of 60+ methods

#### Step 4.3: Update All Tests
- [ ] Update test files to use new Cluster interface
- [ ] Remove dependency on singleton
- [ ] Enable dependency injection in tests

**Before**:
```go
func TestSomething(t *testing.T) {
    cluster := util.EnsureCluster("", "")  // Singleton
    // ...
}
```

**After**:
```go
func TestSomething(t *testing.T) {
    ctx := context.Background()
    cfg := config.Load()
    manager := cluster.NewManager(cfg)
    cl, err := manager.GetOrCreate(ctx, "", "")
    // ...
}
```

**Or with test helper**:
```go
func TestSomething(t *testing.T) {
    cluster := testkit.GetCluster(t)  // Helper that manages lifecycle
    // ...
}
```

**Estimated Time**: 5-7 days
**Risk Level**: High (touches all test files)

---

### 3.5 Phase 5: Organize Tests (Low Risk)

**Goal**: Organize test files into logical packages.

#### Step 5.1: Reorganize Test Files
- [ ] Move tests into domain-specific packages:
  - `tests/healthcheck/`
  - `tests/storage/`
  - `tests/node_configurator/`
  - `tests/data_exporter/`
  - `tests/cleanup/`
- [ ] Create shared test utilities in `pkg/testkit/`
- [ ] Update package names appropriately

#### Step 5.2: Create Test Framework
- [ ] Create `internal/test/framework.go` for test helpers
- [ ] Extract common test patterns
- [ ] Create fixtures for common scenarios

**Estimated Time**: 2-3 days
**Risk Level**: Low (mostly moving files)

---

### 3.6 Phase 6: Cleanup and Documentation (Low Risk)

**Goal**: Remove old code, add documentation, improve developer experience.

#### Step 6.1: Remove Deprecated Code
- [ ] Remove compatibility wrappers
- [ ] Remove old package structure
- [ ] Clean up unused imports
- [ ] Remove global variables

#### Step 6.2: Add Documentation
- [ ] Write package-level documentation
- [ ] Document public APIs
- [ ] Create architecture diagrams
- [ ] Add examples for common use cases

#### Step 6.3: Improve Developer Experience
- [ ] Add clear error messages
- [ ] Improve logging
- [ ] Add validation
- [ ] Create helper functions for common operations

**Estimated Time**: 2-3 days
**Risk Level**: Low (cleanup phase)

---

## 4. Migration Strategy Details

### 4.1 Compatibility Layer Approach

During migration, maintain a compatibility layer that delegates to new implementation:

```go
// Old location: util/kube_storage.go (temporary)
package integration

import (
    newStorage "github.com/deckhouse/sds-e2e/internal/kubernetes/storage"
)

func (cluster *KCluster) CreateLVG(name, nodeName string, bds []string) error {
    // Delegate to new implementation
    return cluster.storageClient.LVMVolumeGroups().Create(
        cluster.ctx,
        newStorage.LVGCreateRequest{
            Name:     name,
            NodeName: nodeName,
            BlockDevices: bds,
        },
    )
}
```

This allows:
- Gradual migration of tests
- Running old and new code side-by-side
- Easy rollback if issues arise
- Zero-downtime refactoring

### 4.2 Testing Strategy

1. **Unit Tests First**: Test new packages in isolation
2. **Integration Tests**: Ensure new code works with existing tests
3. **Parallel Running**: Run old and new implementations in parallel
4. **Gradual Cutover**: Move tests one by one to new implementation

### 4.3 Rollback Plan

At each phase:
- Keep old code in place until new code is proven
- Use feature flags if needed
- Maintain compatibility layer
- Document rollback procedure

---

## 5. Detailed Module Structure

### 5.1 Configuration Module (`internal/config/`)

```
config/
├── config.go           # Main Config struct and Load()
├── env.go              # Environment variable parsing
├── flags.go            # CLI flag definitions
├── cluster_types.go    # Cluster type definitions and validation
├── images.go           # OS image URL definitions
└── defaults.go         # Default values
```

**Responsibilities**:
- Configuration loading from flags, env vars, files
- Configuration validation
- Type-safe configuration access
- No business logic

### 5.2 Cluster Module (`internal/cluster/`)

```
cluster/
├── manager.go          # Cluster manager (replaces EnsureCluster singleton)
├── cluster.go          # Cluster implementation
├── client.go           # Kubernetes client factory
├── cache.go            # Cluster caching logic
└── types.go            # Cluster-related types
```

**Responsibilities**:
- Cluster lifecycle management
- Client initialization and caching
- Context management
- No resource operations (delegates to kubernetes clients)

### 5.3 Kubernetes Module (`internal/kubernetes/`)

```
kubernetes/
├── client.go           # Base client setup and scheme registration
├── core/
│   ├── namespace.go
│   ├── node.go
│   ├── pod.go
│   └── service.go
├── apps/
│   ├── deployment.go
│   └── daemonset.go
├── storage/
│   ├── client.go       # Storage client interface
│   ├── blockdevice.go
│   ├── lvmvolumegroup.go
│   ├── pvc.go
│   └── storageclass.go
├── virtualization/
│   ├── client.go
│   ├── vm.go
│   ├── vdisk.go
│   ├── vmbd.go
│   └── cluster_virtual_image.go
└── deckhouse/
    ├── client.go
    ├── modules.go
    ├── nodegroups.go
    └── staticinstance.go
```

**Responsibilities**:
- All Kubernetes API operations
- Resource-specific logic
- Filtering and querying
- CRUD operations
- No infrastructure concerns (SSH, VM provisioning handled elsewhere)

### 5.4 Infrastructure Module (`internal/infrastructure/`)

```
infrastructure/
├── ssh/
│   ├── client.go       # SSH client implementation
│   ├── factory.go      # SSH client factory
│   ├── keys.go         # SSH key generation
│   └── tunnel.go       # SSH tunnel management
└── vm/
    ├── provider.go     # VM provider interface
    └── deckhouse/
        ├── provider.go # Deckhouse VM provider
        └── installer.go # Deckhouse installation logic
```

**Responsibilities**:
- SSH connection management
- VM provisioning (via providers)
- Infrastructure setup
- No Kubernetes operations (uses kubernetes clients)

### 5.5 Test Module (`internal/test/`)

```
test/
├── framework.go        # Test framework and helpers
├── filters.go          # Filter implementations
├── runner.go           # Test execution runner
├── node_context.go     # Node test context
└── fixtures.go         # Test fixtures
```

**Responsibilities**:
- Test execution utilities
- Filter implementations
- Test context management
- Node-specific test helpers

### 5.6 Public API (`pkg/`)

```
pkg/
├── cluster/
│   ├── interface.go    # Public Cluster interface
│   └── config.go       # Public config types
└── testkit/
    ├── test.go         # Public test helpers
    └── fixtures.go     # Public fixtures
```

**Responsibilities**:
- Public API for external consumers
- Stable interfaces
- Well-documented
- Backward compatibility guarantees

---

## 6. Key Design Decisions

### 6.1 Why Internal Packages?

- **Encapsulation**: Internal packages cannot be imported outside the module
- **Flexibility**: Can refactor internal packages without breaking external API
- **Clear Boundaries**: Makes it obvious what is public vs private

### 6.2 Why Composition Over Inheritance?

- **Flexibility**: Easier to swap implementations
- **Testability**: Can mock individual components
- **Single Responsibility**: Each client has one job

### 6.3 Why Interface-Based Design?

- **Testability**: Easy to create mocks
- **Extensibility**: Can add new implementations
- **Dependency Inversion**: High-level code doesn't depend on low-level details

### 6.4 Why Separate Infrastructure?

- **Clear Boundaries**: Infrastructure is separate from business logic
- **Testability**: Can mock infrastructure in tests
- **Flexibility**: Can swap VM providers, SSH implementations, etc.

---

## 7. Migration Checklist

### Phase 1: Foundation
- [ ] Extract configuration to `internal/config/`
- [ ] Extract utilities to `internal/utils/`
- [ ] Extract filters to `internal/test/filters.go`
- [ ] Extract logging to `internal/logger/`
- [ ] All existing tests still pass

### Phase 2: Kubernetes Clients
- [ ] Create `internal/kubernetes/` structure
- [ ] Extract all K8s operations to appropriate packages
- [ ] Create client interfaces
- [ ] Update KCluster to use composition
- [ ] All existing tests still pass

### Phase 3: Infrastructure
- [ ] Extract SSH to `internal/infrastructure/ssh/`
- [ ] Extract VM operations to `internal/infrastructure/vm/`
- [ ] Create provider interfaces
- [ ] All existing tests still pass

### Phase 4: Cluster Management
- [ ] Create Cluster Manager
- [ ] Create Cluster interface
- [ ] Refactor KCluster implementation
- [ ] Update all tests to use new interface
- [ ] All tests still pass

### Phase 5: Test Organization
- [ ] Reorganize test files
- [ ] Create test framework
- [ ] Update package names
- [ ] All tests still pass

### Phase 6: Cleanup
- [ ] Remove deprecated code
- [ ] Add documentation
- [ ] Improve error messages
- [ ] Final verification

---

## 8. Benefits of New Architecture

### 8.1 Maintainability
- **Clear Structure**: Easy to find code
- **Single Responsibility**: Each package has one job
- **Documented**: Clear purpose for each module

### 8.2 Testability
- **Mockable**: Can mock dependencies via interfaces
- **Isolated**: Test individual components
- **Fast**: Unit tests run quickly

### 8.3 Extensibility
- **Pluggable**: Can add new VM providers, storage backends, etc.
- **Modular**: Can add new features without touching existing code
- **Interface-Based**: New implementations satisfy existing interfaces

### 8.4 Developer Experience
- **Clear API**: Public interfaces are well-defined
- **Better Errors**: Structured error handling
- **Documentation**: Each package is documented
- **Examples**: Common patterns documented

### 8.5 Performance
- **Efficient**: No unnecessary allocations
- **Cached**: Client reuse via manager
- **Context-Aware**: Proper context propagation for cancellation

---

## 9. Risks and Mitigations

### Risk 1: Breaking Existing Tests
**Mitigation**: 
- Maintain compatibility layer
- Gradual migration
- Extensive testing at each phase

### Risk 2: Time Investment
**Mitigation**:
- Phased approach (can stop at any phase)
- Parallel development possible
- Each phase delivers value

### Risk 3: Learning Curve
**Mitigation**:
- Good documentation
- Clear examples
- Code reviews and knowledge sharing

### Risk 4: Over-Engineering
**Mitigation**:
- Start with minimum viable structure
- Add complexity only when needed
- Keep it simple

---

## 10. Success Criteria

1. **All existing tests pass** after refactoring
2. **No performance regression** (ideally improvement)
3. **Code is easier to understand** (measured by code review time)
4. **New features are easier to add** (measured by time to implement)
5. **Tests are easier to write** (measured by lines of test code)
6. **Documentation is comprehensive** (all public APIs documented)

---

## 11. Next Steps

1. **Review this document** with team
2. **Prioritize phases** based on immediate needs
3. **Create GitHub issues** for each phase
4. **Start with Phase 1** (lowest risk)
5. **Iterate and adjust** based on learnings

---

## Appendix A: Current vs Proposed Structure Comparison

### Current Structure Issues

```
❌ Everything in one package
❌ Global state everywhere
❌ 60+ methods on one struct
❌ Mixed concerns
❌ Hard to test
❌ Circular dependencies
```

### Proposed Structure Benefits

```
✅ Clear package boundaries
✅ Structured configuration
✅ Interface-based design
✅ Separated concerns
✅ Easy to test
✅ No circular dependencies
```

---

## Appendix B: Code Examples

### Example 1: Using New Cluster Interface

```go
// tests/storage/pvc_test.go
package storage

import (
    "context"
    "testing"
    
    "github.com/deckhouse/sds-e2e/pkg/cluster"
    "github.com/deckhouse/sds-e2e/pkg/testkit"
)

func TestPVCCreate(t *testing.T) {
    ctx := context.Background()
    
    // Get cluster via testkit helper (manages lifecycle)
    cl := testkit.GetCluster(t)
    defer cl.Close()
    
    // Use typed client interfaces
    pvc, err := cl.Storage().PersistentVolumeClaims().Create(ctx, testkit.PVCSpec{
        Name:      "test-pvc",
        Namespace: testkit.TestNS,
        Size:      "1Gi",
        StorageClass: "test-lvm-thick",
    })
    if err != nil {
        t.Fatal(err)
    }
    
    // Wait for ready
    err = cl.Storage().PersistentVolumeClaims().WaitReady(ctx, pvc.Name, 30*time.Second)
    if err != nil {
        t.Fatal(err)
    }
}
```

### Example 2: Using Configuration

```go
// internal/config/config.go
package config

type Config struct {
    TestNS        string
    NestedCluster NestedClusterConfig
    // ...
}

func Load() *Config {
    cfg := &Config{
        TestNS: getTestNS(),
        // ...
    }
    return cfg
}

// Usage
cfg := config.Load()
cluster := cluster.NewManager(cfg)
```

### Example 3: Mocking for Tests

```go
// internal/kubernetes/storage/mock.go (generated)
type MockLVMVolumeGroupClient struct {
    CreateFunc func(ctx context.Context, req LVGCreateRequest) error
    // ...
}

func (m *MockLVMVolumeGroupClient) Create(ctx context.Context, req LVGCreateRequest) error {
    return m.CreateFunc(ctx, req)
}

// In test
func TestLVGCreate(t *testing.T) {
    mockClient := &MockLVMVolumeGroupClient{
        CreateFunc: func(ctx context.Context, req LVGCreateRequest) error {
            // Test-specific behavior
            return nil
        },
    }
    // Use mock in test
}
```

---

## Conclusion

This architecture refactoring will transform the codebase from a monolithic "pasta code" structure into a clean, maintainable, and testable modular architecture. The phased approach minimizes risk while delivering incremental value.

The key principles:
1. **Separation of Concerns**: Each package has one responsibility
2. **Interface-Based Design**: Easy to test and extend
3. **Dependency Injection**: No globals, proper lifecycle management
4. **Clear Boundaries**: Internal vs public API
5. **Gradual Migration**: Low risk, incremental progress

With this structure, the codebase will be:
- **Easier to understand** (clear package organization)
- **Easier to test** (mockable interfaces)
- **Easier to extend** (modular design)
- **Easier to maintain** (single responsibility)

Start with Phase 1 and iterate based on learnings!

