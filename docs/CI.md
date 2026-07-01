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
| `bootstrap` | resolve | always when reached | `e2e-prune-workspace.sh` + `go run ./cmd/bootstrap-cluster`. For `commander` this only talks to the Commander API (create + wait Ready) — no kubeconfig artifact |
| `run-tests` | resolve, bootstrap | bootstrap succeeded | `e2e-run-tests.sh` (`go mod replace` + `go test`). For `commander`, a gated step injects the connection env (`TEST_CLUSTER_CREATE_MODE=commanderConnect` + `E2E_COMMANDER_*`); enable-modules (`go run ./cmd/enable-modules`) and the suite each connect **in-process** via the commander connector (SSH to the master through the bastion, kubeconfig fetched off the master, API tunnel) — no artifact, no external tunnel |
| `teardown` | resolve, bootstrap, run-tests | `always() && bootstrap succeeded && keep_cluster != 'true'` | `e2e-prune-workspace.sh` + `go run ./cmd/remove-cluster` |

> **Provider neutrality.** All commander-specific behavior lives in
> commander-gated steps; the shared `bootstrap`/`run-tests`/`teardown` jobs are
> unchanged for other providers. Module enablement is **not** a separate job — it
> is a commander-only step inside `run-tests` (mirroring how the other flows
> enable modules in-process via `EnableAndConfigureModules` + `cluster_config`),
> so there is no cross-job dependency that could skip `run-tests` for dvp.
>
> **DVP note.** For `cluster_provider: dvp`, every commander step is skipped, so
> `run-tests` runs with its default (non-commander) environment. The kubeconfig
> hand-off + in-process module enablement above are implemented for `commander`.

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
| `cluster_provider` | provider for bootstrap/teardown: `dvp` or `commander` | `dvp` |
| `module_image_tag` | image tag for the module under test, exposed to `enable-modules` as `E2E_MODULE_IMAGE_TAG` (reference it from `cluster_config` as `modulePullOverride: "${E2E_MODULE_IMAGE_TAG}"`) | `""` |
| `storage_e2e_ref` | git ref of storage-e2e to checkout | `main` |
| `runner_labels` | JSON array of runner labels | `["self-hosted","regular"]` |
| `test_timeout` | Ginkgo suite timeout | `90m` |

## Cluster providers

`bootstrap` and `teardown` run `go run ./cmd/{bootstrap,remove}-cluster`, which
dispatch on `E2E_TEST_CLUSTER_PROVIDER` (set from the `cluster_provider` input)
to a registered provider:

- **`dvp`** (default) — provisions a nested cluster on a base Deckhouse
  Virtualization cluster (needs the `E2E_DVP_BASE_CLUSTER_*` SSH/kubeconfig
  secrets, passed inline as content — the base64 kubeconfig is decoded in the
  workflow, no temp files).
- **`commander`** — creates a fresh cluster through the Deckhouse Commander API
  from a template (`Bootstrap`) and deletes it (`Remove`). It talks to Commander
  over HTTPS and needs neither SSH key nor kubeconfig, so the DVP credential env
  is simply empty for it. The cluster name is the per-PR identity
  `e2e-<module_slug>-pr<pr_number>`, so bootstrap and teardown act on the same
  cluster.

## Required secrets / vars (inherited)

### DVP provider (`cluster_provider: dvp`)

| Secret | Required | Purpose |
|--------|----------|---------|
| `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY` | Yes | SSH private key **content** for the base (virtualization) cluster; passed inline (no temp file) |
| `E2E_DVP_BASE_CLUSTER_KUBECONFIG` | Yes | **base64-encoded** kubeconfig; the workflow decodes it inline and passes the content directly |
| `E2E_DVP_BASE_CLUSTER_SSH_USER` | Yes | SSH user |
| `E2E_DVP_BASE_CLUSTER_SSH_HOST` | Yes | SSH host |
| `E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE` | No | SSH key passphrase |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST` / `_SSH_JUMP_USER` / `_SSH_JUMP_PRIVATE_KEY` | No | jump/bastion host — **all-or-nothing**: set all three together or none (a partial config fails validation). Currently **not wired** in the workflow; CI connects directly. |

### Commander provider (`cluster_provider: commander`)

| Secret / var | Required | Purpose |
|--------------|----------|---------|
| `secrets.E2E_COMMANDER_URL` | Yes | Commander API base URL |
| `secrets.E2E_COMMANDER_TOKEN` | Yes | Commander API token |
| `secrets.E2E_COMMANDER_TEMPLATE_NAME` | Yes | cluster template to create from |
| `secrets.E2E_COMMANDER_CA_CERT` | No | path to a CA cert for the Commander TLS |
| `vars.E2E_COMMANDER_TEMPLATE_VERSION` | No | pin a template version (name or ID); default = current |
| `vars.E2E_COMMANDER_REGISTRY_NAME` | No | registry name resolved to `registry_id` |
| `vars.E2E_COMMANDER_VALUES` | No | JSON template input values (`prefix` is set automatically) |
| `vars.E2E_COMMANDER_AUTH_METHOD` | No | `x-auth-token` (default) / `bearer` / `basic` |
| `vars.E2E_COMMANDER_API_PREFIX` | No | API prefix, default `/api/v1` |
| `vars.E2E_COMMANDER_INSECURE_SKIP_TLS_VERIFY` | No | `true` to skip TLS verify, default `false` |
| `vars.E2E_COMMANDER_WAIT_TIMEOUT` | No | Go duration for the Ready wait, default `30m` |

The cluster name (`E2E_COMMANDER_CLUSTER_NAME`) is set by the workflow to the
per-PR namespace and must not be overridden.

### Common

| Secret | Required | Purpose |
|--------|----------|---------|
| `E2E_TEST_CLUSTER_PROVIDER` | No | provider mode override (default `dvp`); normally set from the `cluster_provider` input |
| `GOPROXY` | No | Go module proxy |

## Scripts

| Script                                   | Used by             | Responsibility                                                                                                  |
|------------------------------------------|---------------------|-----------------------------------------------------------------------------------------------------------------|
| `.github/scripts/e2e-resolve-labels.sh`  | resolve             | PR labels → `keep_cluster` / `ginkgo_filter` / `namespace`                                                      |
| `.github/scripts/e2e-prune-workspace.sh` | bootstrap, teardown | Prune stale Go-cache trees from the self-hosted workspace (credentials are passed as inline content, not files) |
| `.github/scripts/e2e-run-tests.sh`       | run-tests           | self-aware `go mod replace` + `go test` (no SSH tunnel)                                                         |

Tests for these scripts live in `.github/scripts/tests/`.

## Enabling e2e in a module

1. Copy `.github/templates/e2e-tests.yml` into your module at `.github/workflows/e2e-tests.yml`.
2. Adjust `module_slug`, `module_path`, `test_package`, `cluster_config`.
3. Provide the inherited secrets above and create the PR labels `e2e/run`, `e2e/keep-cluster`, `e2e/label:*`.

## Notes / current limitations

- `run-tests` is a **skeleton**: only `E2E_GINKGO_LABEL_FILTER` is wired. The full
  cluster/SSH/license env block is deferred until the test library client is defined.
- No SSH tunnel is created in CI — the Go code establishes its own tunnel.
- The `test_timeout` input is reserved for the future full `run-tests` wiring and is **not yet consumed**; the skeleton currently hardcodes the `go test` timeout.
