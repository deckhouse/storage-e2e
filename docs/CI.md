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
| `bootstrap` | resolve | always when reached | `e2e-prune-workspace.sh` + `go run ./cmd/bootstrap-cluster`. For `commander` this creates the cluster (Commander API, wait Ready) **and** enables the modules-under-test from `cluster_config` — connecting **in-process** via the commander connector (SSH to the master through the bastion, kubeconfig fetched off the master, API tunnel). No kubeconfig artifact, no separate enable-modules step |
| `run-tests` | resolve, bootstrap | bootstrap succeeded | `e2e-run-tests.sh` (`go mod replace` + `go test`). A provider-gated step injects the suite connection env: for `dvp` — `E2E_TEST_CLUSTER_PROVIDER=dvp` + `E2E_DVP_BASE_CLUSTER_*` (consumed by `e2e.Connect`); for `commander` — `E2E_TEST_CLUSTER_PROVIDER=commander` + `E2E_COMMANDER_*` (legacy connector path). The suite attaches **in-process** — no kubeconfig artifact, no external tunnel |
| `teardown` | resolve, bootstrap, run-tests | `always() && resolve succeeded && keep_cluster != 'true'` | `e2e-prune-workspace.sh` + `go run ./cmd/remove-cluster` |

> **Provider neutrality.** All commander-specific behavior lives in
> commander-gated steps; the shared `bootstrap`/`run-tests`/`teardown` jobs are
> unchanged for other providers. Module enablement is **not** a separate job — it
> is a commander-only step inside `run-tests` (mirroring how the other flows
> enable modules in-process via `EnableAndConfigureModules` + `cluster_config`),
> so there is no cross-job dependency that could skip `run-tests` for dvp.
>
> **DVP note.** For `cluster_provider: dvp`, the dvp-gated step injects the
> `pkg/e2e` SDK connection env (`E2E_TEST_CLUSTER_PROVIDER`,
> `E2E_CLUSTER_CONFIG_YAML_PATH`, `E2E_DVP_BASE_CLUSTER_*`), and the suite
> attaches via `e2e.Connect`. The in-process module enablement above is
> implemented for `commander`.

Neither `run-tests` nor `bootstrap` blocks teardown by its result — the cluster
is cleaned regardless of test pass/fail **and even if bootstrap failed** (a failed
bootstrap may leave a partial cluster to remove), unless the `e2e/keep-cluster`
label is set. Teardown only requires `resolve` to have succeeded, since it needs
the resolved `namespace` / `keep_cluster` outputs.

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

The provider is deliberately absent from that identity: `dvp` names a namespace in
the DVP base cluster while `commander` names a cluster in Commander, so the two
never refer to the same thing.

## Running two providers for one PR

A module may call this workflow more than once per pull request — typically once
per provider, with complementary Ginkgo filters, when part of its suite needs
infrastructure only one provider has (a disk it can attach) and the rest needs
only a node shell. Those calls run **in parallel**, not one after the other:

- the concurrency group is keyed by `(module_slug, cluster_provider, pr_number)`,
  so a second provider does not queue behind the first — while a re-push of the
  same provider still serialises, which is what the group is for (it protects the
  shared namespace / cluster name);
- the E2E log artifact carries the provider in its name, because artifact names
  must be unique within a workflow run and both pipelines report into the same
  run of the caller's workflow.

What is *not* handled here is runner supply: with `runner_labels` pointing at a
single self-hosted runner the two pipelines still take turns. Give the label at
least two runners if the parallelism is meant to be real.

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
| `extra_env` | module-specific suite environment, newline-separated `KEY=VALUE` (see [Module-specific environment](#module-specific-environment)) | `""` |
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

| Secret                                                                            | Required        | Purpose                                                                                                                                                                    |
|-----------------------------------------------------------------------------------|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY`                                            | Yes             | SSH private key **content** for the base (virtualization) cluster; passed inline (no temp file)                                                                            |
| `E2E_DVP_BASE_CLUSTER_KUBECONFIG`                                                 | Yes             | **base64-encoded** kubeconfig; the workflow decodes it inline and passes the content directly                                                                              |
| `E2E_DVP_BASE_CLUSTER_SSH_USER`                                                   | Yes             | SSH user                                                                                                                                                                   |
| `E2E_DVP_BASE_CLUSTER_SSH_HOST`                                                   | Yes             | SSH host                                                                                                                                                                   |
| `E2E_DVP_BASE_CLUSTER_SSH_PASSPHRASE`                                             | No              | SSH key passphrase                                                                                                                                                         |
| `E2E_DVP_DKP_LICENSE_KEY`                                                         | Yes (bootstrap) | DKP registry license token for the dhctl install image; consumed only by bootstrap (validated up front via `ValidateForBootstrap`), not teardown                           |
| `E2E_DVP_REGISTRY_DOCKER_CFG`                                                     | Yes (bootstrap) | **base64** dockercfg embedded into the dhctl bootstrap config; consumed only by bootstrap, not teardown                                                                    |
| `E2E_DVP_BASE_CLUSTER_SSH_JUMP_HOST` / `_SSH_JUMP_USER` / `_SSH_JUMP_PRIVATE_KEY` | No              | jump/bastion host — **all-or-nothing**: set all three together or none (a partial config fails validation). Currently **not wired** in the workflow; CI connects directly. |

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
| `E2E_MODULE_ENV` | No | module-specific **secret** suite environment, `KEY=VALUE` per line (see [Module-specific environment](#module-specific-environment)) |

## Module-specific environment

The pipeline forwards its own connection variables (`E2E_DVP_*`,
`E2E_COMMANDER_*`, …) but knows nothing about the backend an individual module's
suite talks to — csi-nfs points at external NFS servers, csi-huawei at a storage
system, csi-scsi-generic at an iSCSI target. Rather than extending the allowlist
once per module, callers pass their own `KEY=VALUE` pairs:

```yaml
    with:
      module_slug: csi-nfs
      extra_env: |
        E2E_NFS_V3_HOST=${{ vars.E2E_NFS_V3_HOST }}
        E2E_NFS_V4_HOST=${{ vars.E2E_NFS_V4_HOST }}
        E2E_NFS_TLS_HOST=${{ vars.E2E_NFS_TLS_HOST }}
    secrets: inherit
```

Secrets do **not** go in `extra_env` — its values are echoed to the log. Put them
in the module repository's `E2E_MODULE_ENV` secret instead, in the same
`KEY=VALUE` format:

```
E2E_NFS_TLS_CA=<base64 PEM>
E2E_NFS_TLS_CLIENT_CERT=<base64 PEM>
E2E_NFS_TLS_CLIENT_KEY=<base64 PEM>
```

The pipeline reads that secret **by fixed name**, so callers keep using
`secrets: inherit` — a per-module secret *input* would force every caller to
enumerate its secrets explicitly instead. Each value is registered with
`::add-mask::` before use and is never printed.

Both sources are optional and parsed the same way: one `KEY=VALUE` per line,
blank lines and `#` comments ignored, whitespace around the key trimmed, and the
value taken verbatim after the first `=` (so `=` inside a value, such as base64
padding, is fine). Values must be **single-line** — pass PEM or kubeconfig
material base64-encoded. Malformed lines, invalid variable names, and attempts to
override a variable the pipeline sets itself (`E2E_TEST_PACKAGE`,
`E2E_STORAGE_E2E_DIR`, `GOPROXY`, …) fail the step rather than silently
retargeting the run.

## Scripts

| Script                                   | Used by             | Responsibility                                                                                                  |
|------------------------------------------|---------------------|-----------------------------------------------------------------------------------------------------------------|
| `.github/scripts/e2e-resolve-labels.sh`  | resolve             | PR labels → `keep_cluster` / `ginkgo_filter` / `namespace`                                                      |
| `.github/scripts/e2e-prune-workspace.sh` | bootstrap, teardown | Prune stale Go-cache trees from the self-hosted workspace (credentials are passed as inline content, not files) |
| `.github/scripts/e2e-module-env.sh`      | run-tests           | Merge `extra_env` + the `E2E_MODULE_ENV` secret into `$GITHUB_ENV` for the suite                                |
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
