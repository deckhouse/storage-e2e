# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Read the linked docs in `docs/` before making non-trivial changes.

## What this is

`github.com/deckhouse/storage-e2e` — an end-to-end test framework for Deckhouse storage
components. It provisions/manages test clusters (VMs via virtualization, existing clusters,
or Deckhouse Commander), wires up storage modules, and runs Ginkgo-based e2e suites.

**It is imported as a library by other repos** (e.g. `csi-ceph`), so the `pkg/` public API
must stay backward compatible.

- Module path: `github.com/deckhouse/storage-e2e`, Go `1.26`.
- Layers (top → bottom): `tests/` → `pkg/` (public API: `testkit`, `cluster`,
  `clusterprovider`, `commander`, `e2e`, `kubernetes`, `retry`, `storage-e2e`) →
  `internal/` domain logic → `internal/infrastructure` → `k8s client-go`. See
  ARCHITECTURE for the full tree.
- `cmd/bootstrap-cluster` and `cmd/remove-cluster` are standalone CLIs for cluster
  lifecycle outside the test suites.

**Two Go modules.** The repo root (`go.mod`) is the framework/library. The nested
`e2e/` directory is a **separate module** (`.../storage-e2e/e2e`) holding the
framework's own self-test suite — it is **not** covered by `./...` from the root
(run its commands with `cd e2e` first). CI runs it via `.github/workflows/e2e-self-test.yml`.

## Documentation map (`docs/`)

| Doc | Purpose |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Package structure, layers, components, env-var reference. **Source of truth for layout.** |
| [docs/FUNCTIONS_GLOSSARY.md](docs/FUNCTIONS_GLOSSARY.md) | All exported `pkg/` functions, grouped by resource. Check before adding new exported functions. |
| [docs/TODO.md](docs/TODO.md) | Global TODO list (managed via the `/todo` convention below). |
| [docs/TESTS_IMPLEMENTATION_PLAN.md](docs/TESTS_IMPLEMENTATION_PLAN.md) | Plan for unit-test coverage and the CI/CD pipeline (build/vet/test, merge-blocking). |
| [docs/CI.md](docs/CI.md) | Reusable e2e GitHub Actions workflow (`bootstrap` → `run-tests` → `teardown`): job graph, inputs, secrets, and PR-label gating (`e2e/run`). |
| [docs/TESTING_STRATEGY.md](docs/TESTING_STRATEGY.md) | *(to be created)* Testing conventions, unit-vs-e2e boundaries, fakes/httptest patterns, coverage policy. |

Also useful: [README.md](README.md) (quick start, env vars, running suites),
[internal/README.md](internal/README.md), [internal/logger/README.md](internal/logger/README.md).

## Build, vet & test

The `Makefile` mirrors the `Go checks` CI workflow (`.github/workflows/go-checks.yml`) —
prefer it for local checks:

```bash
make build   # go build ./...        — compiles everything incl. e2e suites (refactor-breakage check)
make vet     # go vet ./...
make test    # go test -race -shuffle=on ./internal/... ./pkg/...   — unit tests, no cluster
make cover   # unit tests + coverage.out + total %
make e2e     # prints hints for running an individual e2e suite
```

- **Unit tests** live next to sources under `internal/` and `pkg/`; they are hermetic
  (no SSH, no cluster, no network — use `httptest` and `client-go` fakes).
- **E2E suites** live under `tests/` and require real VMs/clusters + env vars
  (`SSH_HOST`, `DKP_LICENSE_KEY`, `TEST_CLUSTER_CREATE_MODE`, …; full list in README).
  **Do not run them in unit/CI flows.** Run one explicitly, e.g.:
  `go test -timeout=240m -v ./tests/<name> -count=1` (`-count=1` disables the test cache).
  - **Single spec:** add a Ginkgo focus, e.g.
    `go test -timeout=240m -v ./tests/<name> -count=1 -ginkgo.focus="should create virtual machines"`.
  - **Fast local iteration:** `export TEST_CLUSTER_CREATE_MODE=alwaysUseExisting` to run
    against an already-running cluster (no VM provisioning). See README for the SSH/jump-host setup.
- New tests/CI conventions are described in TESTS_IMPLEMENTATION_PLAN.md (and, once it
  exists, TESTING_STRATEGY.md).

## Project conventions (must follow)

These mirror the rules in `.cursor/rules/` — apply them whether or not you use Cursor.

1. **Backward compatibility (`pkg/`).** Removing/renaming an exported symbol, changing a
   signature, or changing observed behavior is a breaking change. **Ask the user first**,
   prefer adding alongside (deprecate, don't delete), and tag the WORKLOG entry with
   `[Possible compatibility break]`.

2. **Functions glossary.** Before adding a new exported `pkg/` function, search
   [docs/FUNCTIONS_GLOSSARY.md](docs/FUNCTIONS_GLOSSARY.md) for an existing one. Reuse/extend
   if it exists; otherwise add the new function to the glossary in the matching section.

3. **Versatile functions.** `pkg/` functions must be general-purpose: return data not
   decisions, no hardcoded names/indices, accept broad inputs (`context`, `rest.Config`)
   and return concrete K8s objects, no empty wrappers.

4. **Architecture sync.** On structural changes (add/remove/rename a package or file, move
   between layers, new cluster mode, new env var, changed core types), update the affected
   sections of [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Only touch affected sections.

5. **`/todo` command.** When a message starts with `/todo`, manage items in
   [docs/TODO.md](docs/TODO.md) (Add / Remove / Check|List / Done).

6. **Commits/PRs.** Do not add AI co-author trailers (no `Co-Authored-By: Claude` /
   "Generated with Claude").

## Creating new e2e tests

Use the template + script (don't hand-roll suites, and don't edit `tests/test-template`):

```bash
cd tests && ./create-test.sh <your-test-name>
```
