# E2E tests

## Quick start guide

### Prerequisites

#### Required exports

```bash
# Passphrase of the private key used to connect to the base cluster
export SSH_PASSPHRASE='passphrase'

# Used in case if the code cannot obtain kubeconfig from master itself because e.g. password is required in sudo 
export KUBE_CONFIG_PATH='/path/to/kubeconfig/file' 
```

#### Running a test example

```bash
go test -v ./tests/cluster_creation -count=1
# count=1 prevents go test from using cached test results
```

