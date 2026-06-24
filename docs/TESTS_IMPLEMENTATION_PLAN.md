# Unit Testing & CI/CD Implementation Plan

> Status: **Wave 1 + Wave 2 landed, CI workflow active.** Remaining work:
> Wave 3 fake-client refactors (optional), enable branch protection so the
> "Build, vet & unit test" check becomes required on `main`. This document
> stays as the source of truth for *what* is covered, *how*, and how CI enforces it.

## 1. Goals

1. Cover the **deterministic core logic** of the project (`pkg/` + `internal/`) with fast,
   hermetic unit tests that need **no SSH access, no Kubernetes cluster, and no network**.
2. Add a **GitHub Actions pipeline** that builds, vets, and runs the unit tests on every
   push and pull request.
3. Make the test job a **required status check** so a PR cannot be merged into `main`
   while tests are red.

### Non-goals

- We are **not** trying to unit-test the end-to-end suites under `tests/` — those
  legitimately require real VMs/clusters and stay as manual/scheduled e2e runs.
- We are **not** aiming for 100% line coverage. The realistic target is high coverage of
  the *pure-logic* surface; the cluster/SSH/virtualization orchestration code is mostly
  I/O glue and is covered by e2e.

---

## 2. Current state

- Module: `github.com/deckhouse/storage-e2e`, Go `1.26`.
- Test framework already in use: standard `testing` + table tests (unit), Ginkgo/Gomega (e2e).
- Existing unit tests (2 files, ~all green today):
  - [internal/config/types_test.go](../internal/config/types_test.go) — `ValidateModulePullOverrides`.
  - [internal/logger/logger_test.go](../internal/logger/logger_test.go) — level parsing, handlers, helpers.
- Only CI present: [gitleaks-scan-on-pr.yml](../.github/workflows/gitleaks-scan-on-pr.yml) (secret scanning). **No build/test CI.**
- `ARCHITECTURE.md` §9.2 explicitly lists "Limited unit test coverage" as known tech debt.

### Key fact that shapes the strategy

Most `pkg/kubernetes/*` and `pkg/cluster/*` functions take a `*rest.Config` (or a concrete
`*virtualization.Client`) and **construct their clients internally**, so they can only be
exercised against a live API server. They are **not** directly unit-testable without a
small refactor (see §5). The first waves of tests therefore focus on the large amount of
**pure / injectable logic** that already exists.

---

## 3. Testing strategy & conventions

- **Test type:** standard library `testing` with table-driven subtests (`t.Run`). Matches
  the existing style in `types_test.go` / `logger_test.go`. No new test deps required for
  the core waves.
- **Hermetic:** no real network. HTTP clients are pointed at `httptest.Server`; Kubernetes
  interactions (later waves) use `client-go`'s fakes (`k8s.io/client-go/kubernetes/fake`,
  `k8s.io/client-go/dynamic/fake`) — already available transitively via `client-go`.
- **No global-state leakage:** `internal/config` reads env into package-level vars at init.
  Tests that touch them must **save and restore** the globals with `t.Cleanup`. A small
  test helper (`withEnvSnapshot`) will encapsulate this.
- **Logger:** `logger.GetLogger()` falls back to `slog.Default()` when uninitialized, so
  code under test that logs (e.g. `retry.Do`) is safe to call directly — no setup needed.
- **Determinism:** time-based code is tested with tiny durations and `context` cancellation
  rather than wall-clock sleeps; randomness (`GenerateRandomSuffix`) is asserted on
  shape/charset/length, not exact value.
- **File naming:** `<source>_test.go` next to the source, same package for white-box tests
  of unexported helpers (e.g. `package kubernetes`, `package cluster`).

### Separating unit tests from e2e in CI

The e2e suites under `tests/` must never run in PR CI (they need clusters). Approach:

- **Primary (chosen):** CI runs an explicit package set: `go test ./internal/... ./pkg/...`.
  The `tests/` directory is excluded from the test run.
- **Compile safety:** CI additionally runs `go vet ./...` and `go build ./...`, which
  *compile* the e2e suites (catching refactor breakage) without *executing* them.
- Alternative considered: build tags (`//go:build e2e`) on the e2e suite files. More robust
  but touches every existing/template e2e file and `create-test.sh`. Deferred unless we
  later want `go test ./...` to be safe by default. (Open question Q1.)

---

## 4. Unit test target inventory (prioritized)

Effort key: **S** ≈ <1h, **M** ≈ a few hours, **L** ≈ day+ (usually needs a refactor).

### Wave 1 — pure logic, zero refactor, highest ROI

| # | Package / file | Functions under test | Notable cases | Effort |
|---|---|---|---|---|
| 1 | `pkg/retry` ([retry.go](../pkg/retry/retry.go)) | `IsRetryable`, `IsSSHConnectionError`, `WithRetryAfter`, `Do`/`DoVoid` | k8s `StatusError` codes (500/501/429, RetryAfter), `io.EOF`, every network/k8s/ssh/webhook string pattern, non-retryable → no retry, success-after-N, `ctx` cancel mid-wait, backoff cap | M |
| 2 | `pkg/kubernetes` ([apply.go](../pkg/kubernetes/apply.go)) | `FindUnsetEnvVars`, `splitYAMLDocuments` | multiple `${VAR}`, dedup, set vs unset (use `t.Setenv`), `---` splitting, trailing/empty docs, leading `---` | S |
| 3 | `pkg/kubernetes` ([modules.go](../pkg/kubernetes/modules.go)) | `convertModuleSpecsToConfigs`, `buildModuleGraph`, `topologicalSortLevels`, `isWebhookConnectionError` | nil settings → empty map, missing dependency error, multi-level ordering, **cycle detection**, reverse-dep correctness | M |
| 4 | `pkg/cluster` ([vms.go](../pkg/cluster/vms.go)) | `getCVMINameFromImageURL`, `getVMNodes`, `GetSetupNode`, `GetNodeIPAddress`, `GenerateRandomSuffix`, `generateCloudInitUserData`/`generateSetupNodeCloudInit` (smoke) | `.img`/`.qcow2` strip, `_`/`.`→`-`, collapse `--`, trim `-`, empty→`image`; VM-vs-baremetal filtering; setup-node nil/role; suffix length & charset; cloud-init contains hostname + pubkey | M |
| 5 | `internal/config` ([types.go](../internal/config/types.go)) | `ClusterNode.UnmarshalYAML`, `ClusterDefinition.UnmarshalYAML`, extend `ValidateModulePullOverrides` | invalid `hostType`, invalid `role`, unknown `osType`, `clusterDefinition:` wrapper vs bare, nil module / empty override | M |
| 6 | `internal/kubernetes/commander` ([client.go](../internal/kubernetes/commander/client.go)) | `mapStatusToPhase`, `base64Encode`, `NewClientWithOptions` validation | every status→phase mapping + unknown passthrough; empty baseURL/token errors; trailing-slash trim on baseURL & apiPrefix; default auth method/prefix; bad `CACertPath` | M |
| 7 | `internal/logger` ([level.go](../internal/logger/level.go)) | `LevelToString` (round-trip with `ParseLevel`) | all levels + default | S |
| 8 | `pkg/testkit` ([stress-tests.go](../pkg/testkit/stress-tests.go)) | `(*Config).Validate`, `DefaultConfig` | each required-field error, per-mode branches, invalid `TestOrder` step, resize/clone size requirements, `SnapshotsPerPVC` defaulting | M |
| 9 | `pkg/kubernetes` ([poll.go](../pkg/kubernetes/poll.go)) | `formatRef`, `sameFinalizers`, `errIfTerminating` | ns vs cluster-scoped ref formatting; finalizer set equality (order, dupes, len); terminating object → error | S |

### Wave 2 — needs `httptest` (no refactor)

| # | Package | Functions under test | Approach | Effort |
|---|---|---|---|---|
| 10 | `internal/kubernetes/commander` | `setAuthHeaders` (all 5 methods), `GetClusterByID` (200/404/500), `ListClustersAPI` (array vs `items`/`data` object vs garbage), `GetClusterByName`, `CreateClusterFromTemplate`, `DeleteClusterByID`, `GetClusterKubeconfigByID` (+`/kubeconfig` 404 → cluster-details fallback), `GetRegistryByName` (exact + partial), `GetClusterConnectionInfo` (connection_hosts/agent_data/legacy precedence) | spin `httptest.Server`, build client with `baseURL=server.URL`, assert request headers/paths and decoded responses | M |

> Note: `WaitForClusterReady` uses a hardcoded 10s ticker → not cheaply unit-testable as-is.
> Either skip it or extract the interval (small refactor, Wave 3). Listed as Q2.

### Wave 3 — small refactors to unlock fake-client tests (optional, higher value-per-effort later)

These functions are good candidates but currently build clients from `*rest.Config`
internally. The proposed refactor is **non-breaking**: keep the existing exported signature,
extract the logic into an unexported helper that accepts an interface
(`kubernetes.Interface` / `dynamic.Interface`), and have the public function build the real
client and delegate. Tests then call the helper with a fake.

| Package | Candidates | Refactor | Effort |
|---|---|---|---|
| `pkg/kubernetes` apply.go | `applyDocument`/`createDocument` via `ApplyClient{dynamicClient, discoveryClient}` (already injectable in-package using `dynamic/fake` + a fake discovery) | none for struct; build fakes in test | M |
| `pkg/kubernetes` pvc/pod/namespace/secrets/storageclass | `WaitForPVCsBound`, `WaitForPodsStatus`, namespace/secret CRUD, default-SC selection | extract `…WithClientset(clientset kubernetes.Interface, …)` helper | L |
| `pkg/cluster` lock.go | `AcquireClusterLock`/`Release`/`IsClusterLocked`/`GetClusterLockInfo` (ConfigMap logic, lock-contention error message) | extract clientset-accepting helpers; test with `kubernetes/fake` | M |
| `internal/kubernetes/commander` | `WaitForClusterReady` | inject ticker interval | S |

### Out of scope for unit tests (covered by e2e / not deterministic)

- `internal/infrastructure/ssh/*` (real SSH/network; `findFreePort` is the only trivially
  testable bit).
- `pkg/cluster/{cluster,setup}.go`, `pkg/cluster/vms.go` orchestration (VM/CVI lifecycle).
- `internal/kubernetes/{deckhouse,storage,virtualization}/*` thin CRD clients.
- `pkg/testkit/{ceph*,storageclass}.go` provisioning flows and `StressTestRunner.Run*` modes.

---

## 5. Refactor principles (for Wave 3, if approved)

- **Additive, not breaking:** public API signatures stay; add internal seams.
- Prefer accepting **client-go interfaces** (`kubernetes.Interface`, `dynamic.Interface`)
  over concrete `*Clientset` so fakes drop in.
- Keep retry/IO wiring in the public function; keep *decision logic* in the helper.
- Each refactor lands with its tests in the same PR.

---

## 6. CI/CD pipeline design

New workflow: `.github/workflows/unit-tests.yml` (additive; coexists with gitleaks).

### Triggers
- `push` → **any branch** (every push gets a green/red signal + coverage so feature
  branches surface failures immediately, not only when a PR is opened)
- `pull_request` → `main` (types: opened, synchronize, reopened) — kept so PRs from
  forks are still exercised even though their push events run under the fork's repo

### Job: `unit-tests` (runs-on `ubuntu-latest`)
Steps:
1. `actions/checkout@v4`
2. `actions/setup-go@v5` with `go-version: '1.26'`, `cache: true` (module + build cache).
3. `go mod download`
4. `go build ./...` — compiles everything incl. e2e suites (refactor-breakage guard).
5. `go vet ./...`
6. `go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./internal/... ./pkg/...`
7. `go tool cover -func=coverage.out | tail -1` (print total %).
8. Upload `coverage.out` as an artifact (`actions/upload-artifact@v4`).

### Proposed workflow skeleton

```yaml
name: Unit Tests
on:
  push: {}
  pull_request:
    branches: [main]
    types: [opened, synchronize, reopened]
permissions:
  contents: read
jobs:
  unit-tests:
    name: Build, vet & unit test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true
      - run: go mod download
      - run: go build ./...
      - run: go vet ./...
      - name: Unit tests (race + coverage)
        run: |
          go test -race -shuffle=on -covermode=atomic \
            -coverprofile=coverage.out ./internal/... ./pkg/...
          go tool cover -func=coverage.out | tail -1
      - uses: actions/upload-artifact@v4
        with:
          name: coverage
          path: coverage.out
```

### Optional add-ons (decide in review — Q3)
- **golangci-lint** job (`golangci/golangci-lint-action`) with a checked-in `.golangci.yml`.
  Improves quality gate; adds maintenance. Could start in "report-only" (non-blocking).
- **Coverage threshold** gate (fail under N%). Recommend starting **without** a hard
  threshold and only printing total, then introducing a floor once Wave 1–2 land.
- **Codecov/coveralls** upload for PR coverage comments (needs token/secret).

### Making it block merges
Workflow files alone don't block merges — that's a repo setting:

- **Branch protection** (or a ruleset) on `main`: require status check **"Build, vet & unit
  test"** to pass, and require branches up to date before merging.
- Configure in GitHub Settings → Branches, or via the one-time `gh` command below
  (needs repo-admin rights). The check name must match the `job.name` from
  [.github/workflows/unit-tests.yml](.github/workflows/unit-tests.yml) verbatim.

```bash
# Replace <owner>/<repo> as appropriate; e.g. deckhouse/storage-e2e.
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  /repos/<owner>/<repo>/branches/main/protection \
  -f required_status_checks.strict=true \
  -f 'required_status_checks.contexts[]=Build, vet & unit test' \
  -f enforce_admins=false \
  -F required_pull_request_reviews= \
  -F restrictions=
```

After applying, a PR with a failing unit-test job is blocked from merge.

---

## 7. Developer ergonomics (optional but recommended)

Add a `Makefile` so local + CI use the same commands:

```make
test:        ; go test -race -shuffle=on ./internal/... ./pkg/...
cover:       ; go test -covermode=atomic -coverprofile=coverage.out ./internal/... ./pkg/... && go tool cover -func=coverage.out
vet:         ; go vet ./...
e2e:         ; @echo "run a specific suite, e.g. go test -timeout=240m -v ./tests/<name> -count=1"
```

This also gives the e2e workflow (future) a single entry point.

---

## 8. Rollout phases & milestones

| Phase | Contents | Status |
|---|---|---|
| **P0** | `unit-tests.yml` + `Makefile`; runs existing test files | **Done** — workflow on every push to any branch + PRs to `main` |
| **P1** | Wave 1 (targets #1–#9) | **Done** — `retry` 94%, `commander` mappers, config YAML, `cluster/vms` helpers, `apply`/`modules`/`poll`, `testkit.Validate`, `logger/level` |
| **P2** | Wave 2 (httptest, target #10) | **Done** — Commander HTTP client covered via `httptest.Server` |
| **P3** | Branch protection enabled; check becomes **required** | **Pending admin** — see "Making it block merges" above for the exact `gh api` invocation |
| **P4** *(optional)* | Wave 3 refactors + fake-client tests; lint job | Not started |

Suggested coverage signal (not a hard gate initially): after P2, expect solid coverage of
the pure-logic packages (`pkg/retry`, the pure parts of `pkg/kubernetes`,
`internal/kubernetes/commander`, `internal/config`, `internal/logger`).

---

## 9. Risks & considerations

- **Global env state in `internal/config`** — tests must snapshot/restore package vars;
  `t.Setenv` only affects new reads, not the already-initialized globals. The
  `withEnvSnapshot` helper mitigates this. `-shuffle=on` will surface any ordering bugs.
- **`-race`** roughly doubles test time but is cheap here (no heavy tests) and valuable for
  `retry.Do` / parallel module config goroutines.
- **Module downloads in CI** — first run pulls k8s/virtualization deps; `setup-go` caching
  keeps subsequent runs fast.
- **e2e accidentally running in CI** — avoided by the explicit `./internal/... ./pkg/...`
  package set; `go vet ./...` still compiles them.
- **Hidden behavior change** — any Wave 3 refactor is additive and shipped with tests, but
  reviewers should confirm the public signatures are unchanged.

---

## 10. Open questions for review

- **Q1.** Separate e2e via explicit package list (proposed) or introduce `//go:build e2e`
  tags so `go test ./...` is safe everywhere?
- **Q2.** Refactor `WaitForClusterReady` (and similar tickers) to inject interval for
  testability, or leave time-based waits to e2e?
- **Q3.** Add golangci-lint now (blocking? report-only?) or defer?
- **Q4.** Who enables branch protection on `main` (needs admin), and do you want me to
  provide the exact `gh api` command?
- **Q5.** Do you want a coverage threshold gate eventually, and at what %?
- **Q6.** Coverage reporting service (Codecov) — desired, or keep coverage as a CI artifact only?

---

## 11. Proposed PR breakdown

1. **PR-1 (CI bootstrap):** `unit-tests.yml` + `Makefile`. No new tests. Lands the pipeline.
2. **PR-2 (Wave 1a):** `retry`, `apply`, `modules`, `poll` tests.
3. **PR-3 (Wave 1b):** `cluster/vms` pure helpers, `config/types` YAML, `commander` mappers,
   `logger/level`, `testkit` `Validate`.
4. **PR-4 (Wave 2):** Commander httptest suite.
5. **PR-5 (admin):** enable branch protection / required check.
6. **PR-6+ (optional):** Wave 3 refactors + fake-client tests, lint job.
