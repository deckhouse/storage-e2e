# E2E Tests

This package contains end-to-end tests for SDS (Storage for Deckhouse Services).

## Architecture

The package follows a clean, modular architecture:

- **`internal/config/`** - Configuration management (environment variables, cluster definitions, module configs)
- **`internal/cluster/`** - Cluster lifecycle management (manager, builder)
- **`internal/kubernetes/`** - Kubernetes API clients (virtualization, deckhouse modules)
- **`internal/infrastructure/`** - Infrastructure operations (SSH, VM provisioning)
- **`internal/logger/`** - Logging utilities
- **`internal/utils/`** - Utility functions (retry, crypto)
- **`pkg/cluster/`** - Public cluster interface
- **`pkg/testkit/`** - Public test helpers
- **`tests/`** - Test files using Ginkgo

## Cluster Creation Workflow

The cluster builder implements the following workflow:

1. **Connect to Base Cluster**: Get kubeconfig of the base Deckhouse cluster and connect via SSH
2. **Enable Virtualization Module**: Enable Deckhouse Virtualization Platform module on base cluster
3. **Create Virtual Machines**: Create VMs as defined in cluster configuration
4. **Deploy Deckhouse**: Connect to master VM via SSH and deploy Deckhouse Kubernetes Platform
5. **Get Kubeconfig**: Retrieve kubeconfig of the nested cluster
6. **Enable Modules**: Enable and configure required modules in the nested cluster

## Quick start - Running tests
// TODO amarkov: I strongly recommend add a full example how to run tests with all environments, arguments and commands.

## Writing Tests

Tests are written using Ginkgo. Keep test files simple - they should only contain test logic. Business logic is in other modules.

Example:

```go
var _ = Describe("Cluster Creation", func() {
    var (
        ctx          context.Context
        baseCluster  cluster.Cluster
        testCluster  cluster.Cluster
        clusterCfg   *config.DKPClusterConfig
    )

    BeforeEach(func() {
        ctx = context.Background()
        baseCluster, _ = testkit.GetCluster(ctx, cfg.BaseCluster.KubeConfig, "")
        clusterCfg = &config.DKPClusterConfig{
            // Define your cluster configuration
        }
    })

    It("should create a nested Kubernetes cluster", func() {
        testCluster, err := testkit.BuildTestCluster(ctx, baseCluster, clusterCfg)
        Expect(err).NotTo(HaveOccurred())
        
        err = testCluster.EnsureReady(ctx)
        Expect(err).NotTo(HaveOccurred())
    })
})
```

## Configuration

Configuration is loaded from environment variables:

- `BASE_KUBECONFIG` - Path to base cluster kubeconfig
- `BASE_SSH_HOST` - Base cluster SSH host
- `BASE_SSH_USER` - Base cluster SSH user
- `BASE_SSH_KEY` - Base cluster SSH key path
- `NESTED_KUBECONFIG` - Path for nested cluster kubeconfig
- `NESTED_SSH_HOST` - Nested cluster SSH host
- And more... See `internal/config/config.go` for full list

## Running Tests

```bash
go test ./tests/... -v
```

Or with Ginkgo:

```bash
ginkgo ./tests/...
```

## Structure

- `tests/` - Test files
- `internal/` - Internal packages (not importable outside)
- `pkg/` - Public API (importable)

## Notes

- The code is independent of legacy code (no imports from `legacy/`)
- Test files are simple and focus on test logic only
- Business logic is in separate modules
- The architecture allows for easy extension and testing
