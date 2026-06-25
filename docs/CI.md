# Reusable E2E CI pipeline (storage-e2e)

All pipeline logic lives in `.github/workflows/e2e.yml` — a reusable
(`workflow_call`) workflow. Consumer modules add a thin caller workflow that
gates on the `e2e/run` PR label and calls it with `secrets: inherit`.

## Job graph

```
resolve ──> bootstrap ──> run-tests ──> teardown
```

| Job | `needs` | Runs when | What it does |
|-----|---------|-----------|--------------|
| `resolve` | — | always (workflow invoked) | Sparse-checks-out `.github/scripts`, runs `e2e-resolve-labels.sh` → outputs `keep_cluster`, `ginkgo_filter`, `namespace` |
| `bootstrap` | resolve | always when reached | `e2e-prepare-creds.sh` + `go run ./cmd/bootstrap-cluster` |
| `run-tests` | resolve, bootstrap | `needs.bootstrap.result == 'success'` | `e2e-run-tests.sh` (`go mod replace` + `go test` with Ginkgo filter); uploads log |
| `teardown` | resolve, bootstrap, run-tests | `always() && bootstrap succeeded && keep_cluster != 'true'` | `e2e-prepare-creds.sh` + `go run ./cmd/remove-cluster` |

`run-tests` does **not** block teardown by its own result — the cluster is
cleaned regardless of test pass/fail, unless the `e2e/keep-cluster` label is set.

## PR labels (Kubernetes/Prow style)

| Label | Effect |
|-------|--------|
| `e2e/run` | **Gate** (in the caller). Without it the reusable workflow is not invoked. |
| `e2e/keep-cluster` | Skip teardown so you can re-run tests on the same cluster. |
| `e2e/label:<x>` | Ginkgo labels; multiple are joined with ` \|\| ` (e.g. `stress-test \|\| integration`). Falls back to the `label_filter` input (default `!stress-test`) when none are present. |

## Stable per-PR identity

Namespace / cluster identity is `e2e-<module_slug>-pr<pr_number>` — **no `run_id`**.
Bootstrap is idempotent (`CreateNamespaceIfNotExists`), so re-runs land in the
same namespace → "same cluster".

## Reusable workflow inputs

| Input | Purpose | Default |
|-------|---------|---------|
| `module_slug` | module name used in the namespace | (required) |
| `module_path` | path to the Go module containing tests | `.` |
| `test_package` | Go package to test | `./tests/` |
| `label_filter` | default Ginkgo filter when no `e2e/label:*` labels | `!stress-test` |
| `cluster_config` | path (in the module repo) to the cluster YAML (`E2E_CLUSTER_CONFIG_YAML_PATH`) | (required) |
| `storage_e2e_ref` | git ref of storage-e2e to checkout | `main` |
| `runner_labels` | JSON array of runner labels | `["self-hosted","regular"]` |
| `test_timeout` | Ginkgo suite timeout | `90m` |

## Required secrets (inherited)

| Secret | Required | Purpose |
|--------|----------|---------|
| `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY` | Yes | SSH key for the base (virtualization) cluster |
| `E2E_DVP_BASE_CLUSTER_KUBECONFIG` | Yes | base64 kubeconfig for the base cluster |
| `E2E_DVP_BASE_CLUSTER_SSH_USER` | Yes | SSH user |
| `E2E_DVP_BASE_CLUSTER_SSH_HOST` | Yes | SSH host |
| `E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE` | No | SSH key passphrase |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST` / `_SSH_JUMP_USER` | No | jump/bastion host |
| `E2E_TEST_CLUSTER_PROVIDER` | No | provider mode (default `dvp`) |
| `GOPROXY` | No | Go module proxy |

## Scripts

| Script | Used by | Responsibility |
|--------|---------|----------------|
| `.github/scripts/e2e-resolve-labels.sh` | resolve | PR labels → `keep_cluster` / `ginkgo_filter` / `namespace` |
| `.github/scripts/e2e-prepare-creds.sh` | bootstrap, teardown | SSH key + kubeconfig secrets → temp files; workspace prune |
| `.github/scripts/e2e-run-tests.sh` | run-tests | self-aware `go mod replace` + `go test` (no SSH tunnel) |

Tests for these scripts live in `.github/scripts/tests/`.

## Enabling e2e in a module

1. Copy `.github/templates/e2e-tests.yml` into your module at `.github/workflows/e2e-tests.yml`.
2. Adjust `module_slug`, `module_path`, `test_package`, `cluster_config`.
3. Provide the inherited secrets above and create the PR labels `e2e/run`, `e2e/keep-cluster`, `e2e/label:*`.

## Notes / current limitations

- `run-tests` is a **skeleton**: only `E2E_GINKGO_LABEL_FILTER` is wired. The full
  cluster/SSH/license env block is deferred until the test library client is defined.
- No SSH tunnel is created in CI — the Go code establishes its own tunnel.
