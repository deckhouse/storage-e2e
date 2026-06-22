# Reusable CI pipeline (storage-e2e)

Three-job workflow with **mocked** `create-cluster` / `teardown-cluster` and a full `run-tests` that mirrors the `build_dev` smoke flow.

## Jobs

| Job | Condition | What it does |
|-----|-----------|--------------|
| `create-cluster` | `pipeline_mode == 'create-and-test'` | No-op placeholder (mocked) |
| `run-tests` | after `create-cluster` succeeds | Sets up SSH tunnel + kubeconfig, runs `go test` directly in the module repo |
| `teardown-cluster` | `pipeline_mode == 'teardown-only'` | No-op placeholder (mocked) |

Cluster lifecycle is handled inside the module's test suite via `pkg/cluster.CreateOrConnectToTestCluster`.

## PR-scoped namespace

`TEST_CLUSTER_NAMESPACE = e2e-<module_slug>-pr<pr_number>-<run_id>` — unique per run, set both in `env:` and forwarded to `go test`.

## How to call from a module repo

```yaml
jobs:
  e2e:
    uses: deckhouse/storage-e2e/.github/workflows/e2e-reusable.yml@<ref>
    secrets: inherit
    with:
      pipeline_mode: create-and-test   # or teardown-only
      pr_number: "123"
      module_slug: sds-node-configurator
      module_path: e2e
      cluster_provider: alwaysCreateNew
      cluster_config: e2e/tests/cluster_config.yml
      test_package: ./tests/
      label_filter: ""          # empty → !stress-test (all non-stress specs)
      test_timeout: 90m
      storage_e2e_ref: main     # storage-e2e branch/tag to checkout and replace
```

## Required secrets (inherited)

| Secret | Required | Purpose |
|--------|----------|---------|
| `E2E_SSH_PRIVATE_KEY` | Yes | SSH key for master node |
| `E2E_SSH_HOST` | Yes | Master node IP/hostname |
| `E2E_SSH_USER` | Yes | SSH user on master |
| `SSH_VM_USER` | No | User for VM nodes (default: `cloud`) |
| `E2E_SSH_JUMP_HOST` | No | Jump/bastion host for `10.10.10.x` networks |
| `E2E_SSH_JUMP_USER` | No | SSH user on jump host |
| `E2E_CLUSTER_KUBECONFIG` | No | Base64 kubeconfig for the virtualization cluster |
| `E2E_TEST_CLUSTER_STORAGE_CLASS` | No | StorageClass for VirtualDisks |
| `E2E_TEST_CLUSTER_CREATE_MODE` | No | Overrides `cluster_provider` input |
| `E2E_DECKHOUSE_LICENSE` | No | DKP license key |
| `E2E_REGISTRY_DOCKER_CFG` | No | Registry auth |
| `GOPROXY` | No | Go module proxy |

## Label filter

- `inputs.label_filter` → `E2E_GINKGO_LABEL_FILTER`
- If empty at runtime → auto-set to `!stress-test` (all specs except stress)
- Minimum suite timeout enforced: 90m

## run-tests flow

1. Checkout module repo + `storage-e2e` at `inputs.storage_e2e_ref`
2. `go mod edit -replace github.com/deckhouse/storage-e2e=./storage-e2e` to use local ref
3. Open SSH tunnel to master (ProxyJump via `E2E_SSH_JUMP_HOST` when set)
4. `go test -v -timeout 3h30m ./tests/ -run '^TestSdsNodeConfigurator$' -ginkgo.label-filter=...`
5. Upload `e2e-test-output.log` as artifact
6. Cleanup: delete test namespace + VMs (SSH tunnel, `if: always()`)
