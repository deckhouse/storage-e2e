# Work Log

All notable changes to this repository are documented here. New entries are appended with date-time.

---

## 2026-06-07

- **Update** `.github/workflows/unit-tests.yml`: integrate GitHub native code coverage (per-push) — add `code-quality: write` + `pull-requests: read` permissions, convert `coverage.out` to Cobertura XML via `boumenot/gocover-cobertura`, and publish with `actions/upload-code-coverage@v1`; coverage artifact now also includes `coverage.xml`

---

## 2026-05-06

- **Add** `UploadPrivate` on `ssh.SSHClient` (`internal/infrastructure/ssh`): SFTP `Chmod` immediately after `Create`, before payload copy; `uploadOverSFTPOnce`, `uploadWithSFTPRetries`, `jumpUploadWithSFTPRetries`; passphrase `BootstrapCluster` uses it with `install -d -m 0700` staging (`pkg/cluster/setup.go`); ARCHITECTURE mentions ssh uploads
- **Bugfix** `ensureVirtualMachineClassForClusterVMs` (`pkg/cluster/vms.go`): GET + wait Ready for configured class including default `generic`; explicit error if default missing; Host CPU auto-clone still clears `nodeSelector`/`tolerations` from template
- **Update** `ValidateEnvironment` (`internal/config/env.go`): non-`generic` `TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME` validated with `IsDNS1123Subdomain`; README, ARCHITECTURE §7, FUNCTIONS_GLOSSARY aligned (names + auto-created class semantics)

---

## 2026-05-04

- **Bugfix** `BootstrapCluster` in `pkg/cluster/setup.go`: drop dhctl-in-Docker flow via `SSH_AUTH_SOCK`/ssh-agent; bind-mount the setup-node key (from `UploadBootstrapFiles`) to `/root/.ssh/id_rsa` and pass `--ssh-agent-private-keys` — aligns with dhctl/lib-connection `ExtractConfig` reading key paths early ([deckhouse#19063](https://github.com/deckhouse/deckhouse/pull/19063))
- **Add** when `SSH_PASSPHRASE` is set: build dhctl connection-config (`SSHConfig` + `SSHHost`, `dhctl.deckhouse.io/v1`) with inline PEM and passphrase, upload to the setup node, run bootstrap with `--connection-config` only (dhctl disallows mixing with `--ssh-*`)
- **Add** `buildDHCTLSSHConnectionConfig` and YAML manifest structs (`dhctlSSHConfigManifest`, etc.) in `pkg/cluster/setup.go`

---

## 2026-04-30

- **Add** `TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME` in `internal/config/env.go`: configurable `VirtualMachineClassName` for base-cluster VMs (default `generic`), DNS-1123 validation for non-generic names
- **Add** `EffectiveVirtualMachineClassName()` and `VirtualMachineClassReadinessTimeout` (`internal/config/config.go`)
- **Add** `VirtualMachineClass` client (`internal/kubernetes/virtualization/virtual_machine_class.go`) and `Client.VirtualMachineClasses()` in `client.go`
- **Add** `ensureVirtualMachineClassForClusterVMs` / readiness wait in `pkg/cluster/vms.go`: if named class is missing, clone from `generic` with `spec.cpu.type` Host, label `storage-e2e.deckhouse.io/auto-created=true`; no deletion on e2e cleanup
- **Update** `CreateVirtualMachines` to call ensure before CVMI creation; `createVM` uses effective class name
- **Update** env dumps in `pkg/cluster/cluster.go`, `tests/test-template/template_test.go`, and `tests/csi-all-stress-tests/csi_all_stress_tests_test.go`
- **Update** `docs/FUNCTIONS_GLOSSARY.md`: `CreateVirtualMachines` description (ensure VM class)
- **Bugfix** `ValidateEnvironment` in `internal/config/env.go`: align error strings with staticcheck ST1005 (no trailing punctuation; semicolons in multi-part messages)
- **Update** `github.com/deckhouse/virtualization/api` to v1.8.0: register `core/v1alpha3` scheme in virtualization client; `VirtualMachineClass` CRUD uses `v1alpha3` (preferred API; `spec.cpu.discovery` is `*CpuDiscovery`, so Host CPU serializes without empty discovery object)

---

## 2026-03-25

- **Refactor** `WaitForLocalStorageClassCreated` in `pkg/kubernetes/localstorageclass.go`: replaced manual deadline + `time.Sleep` with idiomatic `context.WithTimeout` + `time.NewTicker` + `select` pattern
- **Rename** `GetAllNodeNames` -> `GetNodes` in `pkg/kubernetes/nodes.go`: now returns `[]corev1.Node` instead of `[]string`, giving callers access to the full node object
- **Rename** `GetWorkerNodeNames` -> `GetWorkerNodes` in `pkg/kubernetes/nodes.go`: now returns `[]corev1.Node` and reuses `GetNodes` internally instead of making a separate API call
- **Rename** `NodeHasUnschedulableTaint` -> split into `GetNodeTaints` + `IsNodeCordoned` in `pkg/kubernetes/nodes.go`: separates data retrieval from boolean check for better reusability
- **Rename** `StorageClassExists` -> `GetStorageClass` in `pkg/kubernetes/storageclass.go`: now returns `*storagev1.StorageClass` instead of `bool`, allowing callers to inspect the object
- **Refactor** `WaitForStorageClass` in `pkg/kubernetes/storageclass.go`: replaced manual deadline + `time.Sleep` with `context.WithTimeout` + `select` pattern
- **Refactor** `WaitForStorageClassDeletion` in `pkg/kubernetes/storageclass.go`: same `context.WithTimeout` + `select` pattern (note: currently unused/dead code)
- **Bugfix** `EnsureDefaultStorageClass` in `pkg/testkit/storageclass.go`: fixed early return that skipped `SetGlobalDefaultStorageClass` when StorageClass already existed
- **Update** all callers of renamed functions in `pkg/testkit/storageclass.go`
- **Update** `README.md`: fixed broken link to `FUNCTIONS_GLOSSARY.md` (was `pkg/`, now `docs/`)
- **Update** `docs/ARCHITECTURE.md`: actualized package tree (removed `legacy/`, added missing files in `pkg/kubernetes/`, `pkg/retry/`, `pkg/testkit/`, `internal/kubernetes/commander/`, `internal/kubernetes/storage/`, `tests/csi-all-stress-tests/`, `docs/`), fixed sections 3.2/3.3/3.6 file listings, added Commander mode and env vars to section 7, updated section 9 completion status
- **Update** `docs/FUNCTIONS_GLOSSARY.md`: updated renamed functions (`GetNodes`, `GetWorkerNodes`, `GetNodeTaints`, `IsNodeCordoned`, `GetStorageClass`), removed sections for nonexistent files (`storageclass_manage.go`, `volumesnapshotclass.go`)
- **Add** `docs/WORKLOG.md`: created change log for tracking all repository changes
- **Add** `.cursor/rules/worklog.mdc`: rule to append worklog entries on code changes
- **Add** `.cursor/rules/functions-glossary.mdc`: rule to check glossary before adding new exported functions
- **Add** `.cursor/rules/architecture.mdc`: rule to keep architecture docs in sync with structural changes
- **Add** `.cursor/rules/todo-command.mdc`: `/todo` command for managing `docs/TODO.md`
- **Add** `.cursor/rules/backward-compatibility.mdc`: rule to guard backward compatibility of exported `pkg/` API — ask before breaking changes, mark worklog with `[Possible compatibility break]`
- **Add** `.cursor/rules/versatile-functions.mdc`: rule to ensure new functions are general-purpose and reusable — return data not decisions, no hardcoded names, compose from existing functions, no empty wrappers

---

## 2026-05-05

- **Add** `internal/config/overrides.go` + `_test.go`: `ExpandEnvInModulePullOverride` resolves `${VAR}` placeholders in `modulePullOverride` at config load time; missing env fails fast with an explicit error so CI can point modules at `pr<N>` / `mr<N>` images via a single env var (`MODULE_IMAGE_TAG`) without editing `cluster_config.yml`.
- **Update** `internal/cluster/cluster.go::LoadClusterConfig` and `pkg/cluster/cluster.go::loadClusterConfigFromPath`: hook `ExpandEnvInModulePullOverride` right after `yaml.Unmarshal`.
- **Update** `README.md`: documented `${VAR}` form in `modulePullOverride` and the fail-fast behavior on unset env vars.
- **Update** `internal/cluster/cluster.go::GetKubeconfig`: when SSH retrieval of `/etc/kubernetes/{super-admin,admin}.conf` fails, the function now fails fast unless `KUBE_CONFIG_PATH` is set explicitly. The previously considered fallback to `clientcmd.NewDefaultClientConfigLoadingRules` (KUBECONFIG / `~/.kube/config`) was dropped before release to preserve the original fail-fast contract — a silent fallback to the developer's personal kubeconfig is too risky in CI and on machines whose `kubectl` already points at an unrelated cluster.
- **Bugfix** `pkg/cluster/vms.go::generateCloudInitUserData`: pin apt at `mirror.yandex.ru` and force IPv4 (`Acquire::ForceIPv4=true`) in cloud-init, so `package_update` and Docker install stop stalling when `archive.ubuntu.com` IPs are partially unreachable.
- **Refactor** `internal/infrastructure/ssh/client.go::StartTunnel` (both `*client` and `*jumpHostClient`): extracted shared `runTunnelLoop` + `tunnelDialer`. On dial failure that looks like a dropped SSH session, the loop now logs a visible WARN, calls the existing `reconnect()` (retry + exponential backoff), and retries the dial once with the freshly rebuilt session. Fixes the "test hangs 20 minutes silently after Wi-Fi flap" failure mode.
- **Add** `pkg/kubernetes/poll.go`: `pollResourceUntilReady` centralizes the `WaitFor*Ready` loops with a per-call `PollGetTimeout` (30s) on every Get and WARN logging once consecutive Get failures cross 3, so a dropped tunnel surfaces in seconds instead of after the 20-minute readyTimeout.
- **Refactor** `pkg/kubernetes/cephcluster.go`, `pkg/kubernetes/cephblockpool.go`, `pkg/kubernetes/cephfilesystem.go`: `WaitForCephClusterReady` / `WaitForCephBlockPoolReady` / `WaitForCephFilesystemReady` migrated to `pollResourceUntilReady`. Public signatures unchanged.
- **Add** `pkg/kubernetes/pod_exec.go`: `ExecInPod` (pods/exec via SPDY), `ReadFileFromPod` (`cat <path>` wrapper for non-distroless images), and `ReadFileFromDistrolessPod` (single-shot ephemeral container injection that reads through `/proc/1/root<path>` thanks to the shared PID namespace; uses the dedicated `ephemeralcontainers` subresource so the target pod and its sandbox are NOT restarted and `metadata.generation` is not bumped — keeps downstream rollout assertions clean).
- **Add** `pkg/kubernetes/pod_exec.go::DistrolessReader` + `OpenDistrolessReader`: long-lived ephemeral-container session for cheap repeated reads. `(*DistrolessReader).ReadFile` is a plain `pods/exec` round-trip against the already-running ephemeral container; `(*DistrolessReader).PodName()` lets callers detect rollouts and re-open against the new pod. Pays the ephemeral-container cold start once instead of per `Eventually` iteration.
- **Add** `pkg/kubernetes/poll.go::pollResourceUntilGone` + per-CR `WaitForCephClusterGone` / `WaitForCephBlockPoolGone` / `WaitForCephFilesystemGone` / `WaitForCephClusterAuthenticationGone` / `WaitForCephClusterConnectionGone` / `WaitForCephStorageClassGone` helpers. Logs `deletionTimestamp` and finalizers progress periodically so a stuck finalizer is visible immediately. Fail-fast on timeout — no auto-strip of finalizers; the operator must investigate before re-running.
- **Update** Ceph CR `Create*` helpers (`CreateCephCluster` / `CreateCephBlockPool` / `CreateCephFilesystem` / `CreateCephClusterAuthentication` / `CreateCephClusterConnection` / `CreateCephStorageClass`) and `WaitFor*Ready`: now fail fast when the live object has `metadata.deletionTimestamp != nil`. Prevents the framework from updating a Terminating object (silent no-op) or waiting 20 minutes on Ready for an object that's being garbage-collected.
- **Refactor** `pkg/testkit/ceph.go::TeardownCephStorageClass`: explicitly `WaitFor*Gone` after every Delete in the right order (`CephStorageClass` → `CephClusterConnection` → `CephClusterAuthentication` → `CephBlockPool` or `CephFilesystem` → `CephCluster` → `rook-config-override`). Without these waits the parent `CephCluster` was deleted before its dependents were gone, Rook recorded `DeletionIsBlocked / ObjectHasDependents`, and the next test run either found a stuck Terminating CR or hung in `WaitForCephClusterReady`. Errors are aggregated; NotFound is treated as success.
- **Update** `pkg/testkit/ceph_crc.go::RestartCephDaemons`: extended the daemon selector from `mon,mgr,osd` to `mon,mgr,osd,mds,rgw`. A global `ms_crc_data` flip lives in `ceph.conf` and any unrestarted daemon (typically MDS) silently breaks the messenger handshake — degrades CephFS and pins csi-cephfs PVCs in Pending. `rgw` is included for forward-compat with future S3 tests.
- **Add** `pkg/testkit/ceph_crc.go::RestartRookOperator`: rollout-restarts the rook-operator Deployment after a wire-protocol bounce so it picks up the new `ceph.conf` instead of pinning the cephcluster CR in `HEALTH_ERR`. Deployment name is derived from the namespace by stripping the leading `d8-` prefix (Deckhouse module convention, e.g. `d8-sds-elastic` → `sds-elastic`); vanilla Rook is not supported.
- **Update** `pkg/testkit/ceph_crc.go::SetMsCrcDataOnServer`: after rewriting `rook-config-override` the helper now (1) calls `RestartCephDaemons` for the extended selector, (2) calls `RestartRookOperator`, then (3) waits for every `CephFilesystem` in the namespace to come back to Ready. This is what unblocks the CephFS half of the msCrcData matrix — previously a flip silently left MDS / operator out of sync.
- **Update** `docs/FUNCTIONS_GLOSSARY.md`: noted that the three `WaitForCeph*Ready` helpers now apply a per-call deadline and emit WARN on consecutive Get failures.
- **Update** `docs/ARCHITECTURE.md`: added `pkg/kubernetes/poll.go` to Section 1.1 and Section 3.6, added `pkg/kubernetes/cephfilesystem.go`, added `internal/config/overrides.go` to Section 3.1, added `pkg/kubernetes/pod_exec.go` to Section 1.1 and Section 3.6, documented `KUBE_CONFIG_PATH` semantics and `${VAR}` expansion (`MODULE_IMAGE_TAG`) in Section 7.
- **Update** `docs/FUNCTIONS_GLOSSARY.md`: documented `OpenDistrolessReader` + `*DistrolessReader` methods, `CreateStorageClass`, `CreateVolumeSnapshotClass` / `WaitForVolumeSnapshotClass`, `RenderCephGlobalConfig`, and the full `pkg/testkit/ceph_crc.go` surface (`EnableServerCRC` / `DisableServerCRC` / `ResetServerCRCToDefault` / `SetMsCrcDataOnServer` / `RestartCephDaemons` / `RestartRookOperator`); added matching TOC entries.
- **Add** `internal/infrastructure/ssh.SSHClient::ExecCapture`: remote command execution variant that returns stdout and stderr separately while keeping the existing retry/reconnect behavior for direct SSH and jump-host clients. `GetKubeconfig` uses it so kubeconfig YAML stays on stdout and diagnostic text stays available from stderr.
- **Update** `internal/cluster/cluster.go::GetKubeconfig`: when the SSH-side kubeconfig fetch fails and `KUBE_CONFIG_PATH` is unset, the function now classifies the original failed read attempt without extra SSH round-trips. `classifyKubeconfigFetchFailure` checks context cancellation, distinguishes SSH transport failures from remote command exits, and maps stderr to kubeconfig-missing / sudo-password-required / permission-denied causes. The returned error remains structured and actionable, includes the existing `KUBE_CONFIG_PATH` escape hatch, and wraps the original SSH error via `%w`.
- **Bugfix** `internal/cluster/cluster.go::getKubeconfigRemoteShell`: dropped the `sudo -n sh -c '...'` wrapper and now invokes `sudo -n /bin/cat <path>` directly (with a `||` fallback from `super-admin.conf` to `admin.conf`). The wrapper made the privileged binary `/bin/sh`, so the recommended fine-grained `NOPASSWD: /bin/cat /etc/kubernetes/{super-admin,admin}.conf` sudoers rule did not match and `GetKubeconfig` failed with "sudo on the master requires a password" even after the operator pasted the recommended snippet.

---

## 2026-05-13

- **Update** synchronized docs for commit `bc358dff` by adding `pkg/kubernetes.NewVirtualizationClient` and `pkg/storage-e2e.Initialize` to `docs/FUNCTIONS_GLOSSARY.md`, and reflecting new `pkg/kubernetes/virtclient.go` / `pkg/storage-e2e/setup.go` in `docs/ARCHITECTURE.md`

---

## 2026-05-14

- **Update** `pkg/kubernetes/virtualdisk.go`: normalized new reattach API to `ReattachVirtualDiskToVM` (Go initialism style), added input validation and exported docs for `VirtualDiskReattachmentConfig`; synced entry in `docs/FUNCTIONS_GLOSSARY.md`

---

## 2026-05-20

- **Remove** `internal/config/overrides.go`: dropped `${VAR}` expansion in `modulePullOverride` per review — each module needs its own literal tag in `cluster_config.yml` (e.g. `pr131`, `mr55`).
- **Add** `internal/config.ValidateModulePullOverrides`: rejects `${...}` placeholders at config load with an explicit error.
- **Update** `internal/cluster/cluster.go::LoadClusterConfig` and `pkg/cluster/cluster.go::loadClusterConfigFromPath`, `README.md`, `docs/ARCHITECTURE.md`.

---

## 2026-06-03

- **Add** `.github/workflows/unit-tests.yml`: mandatory CI workflow that builds, vets and runs unit tests on every push (any branch) and on PRs to `main`; uses `go-version-file: go.mod`, `-race -shuffle=on -covermode=atomic`, uploads `coverage.out` artifact, scoped to `./internal/... ./pkg/...` so e2e suites stay off CI.
- **Add** `Makefile`: `test` / `cover` / `vet` / `build` / `e2e` / `clean` targets mirroring the CI commands; `.gitignore` for `coverage.out` / `coverage.html`.
- **Add** Wave 1 unit tests (`pkg/retry/retry_test.go`, `pkg/kubernetes/{apply,modules,poll}_test.go`, `pkg/cluster/vms_test.go`, `pkg/testkit/stress_tests_test.go`, `internal/config/types_yaml_test.go`, `internal/kubernetes/commander/client_test.go`, `internal/logger/level_test.go`): hermetic table-driven coverage of `retry.Do/IsRetryable/IsSSHConnectionError/WithRetryAfter`, YAML doc splitting/env-var scanning, module graph + topo sort + cycle detection, `cluster/vms` pure helpers, `commander` mappers / base64 / `NewClientWithOptions` validation, `stress-tests.Config.Validate` / `DefaultConfig`, `LevelToString` round-trip, `ClusterNode`/`ClusterDefinition` YAML unmarshal validation.
- **Add** Wave 2 httptest tests (`internal/kubernetes/commander/client_http_test.go`): drives the Commander HTTP client (`GetClusterByID`, `ListClustersAPI` array/items/data/garbage, `GetClusterByName`, `CreateClusterFromTemplate`, `DeleteClusterByID`, `GetClusterKubeconfigByID` + cluster-details fallback, `GetRegistryByName`, `GetClusterConnectionInfo` precedence + defaults) and all five `setAuthHeaders` paths via a real `httptest.Server`.
- **Update** `docs/TESTS_IMPLEMENTATION_PLAN.md`: triggers changed from `push → main` to push-on-any-branch + `pull_request → main`; status header refreshed; rollout phases marked Done/Pending; exact `gh api` branch-protection command documented.
- **Update** `.github/workflows/gitleaks-scan-on-pr.yml` → renamed to `.github/workflows/gitleaks.yml`: workflow `name` shortened to `Gitleaks`, added `push: {}` trigger so secret scanning runs on every push (any branch), not only on PRs; added cancel-in-progress concurrency group.
- **Update** `.github/workflows/gitleaks.yml`: split into two jobs gated by `github.event_name` — `gitleaks_diff` (`scan_mode: diff`) for `pull_request`, `gitleaks_full` (`scan_mode: full`) for `push`; fixes `fatal: invalid refspec '+refs/pull//merge:...'` that broke push runs because the upstream action's diff mode needs `github.event.number`. Both jobs share check name `Gitleaks scan`.
- **Update** `.github/workflows/gitleaks.yml`: reverted to `pull_request`-only (single `gitleaks_scan` job, `scan_mode: diff`); dropped the `push` trigger because the upstream action's diff mode needs `github.event.number` and fails on push with `fatal: invalid refspec '+refs/pull//merge:...'`.
- **Add** `.gitleaksignore`: ignores the `generic-api-key` false positive on `internal/kubernetes/commander/client_test.go:75` (base64 test fixture) by fingerprint at commit `5f1edc2`; the diff scan flags the introducing commit, so the later inline `gitleaks:allow` could not suppress it.

---

## 2026-06-08

- **Add** `pkg/config/config_test.go`: unit tests for `config.New` covering provider parsing, missing required `TEST_CLUSTER_PROVIDER` (error), empty-value handling, and table-driven provider values.

---

## 2026-06-19

- **Bugfix** `internal/config.ResolveModulePullOverrides`: detect malformed `${...}` on the original string (stripping
  valid refs first) instead of the resolved value, avoiding a false "malformed" error when an env value itself contains
  `${...}`.
- **Add** `pkg/clusterprovider/registry/registry_test.go`: table/unit tests for `Registry` covering `NewRegistry`
  seeding the built-in DVP provider, `Get` for registered/unregistered modes, `Register` add + replace semantics,
  `DefaultRegistry` contents, and a race-detector concurrency test for `Register`/`Get`

## 2026-06-22

- **Add** `.github/workflows/e2e-reusable.yml`: reusable three-job E2E pipeline (`create-cluster` mocked, `run-tests` mirrors `build_dev` flow, `teardown-cluster` mocked); SSH tunnel, `go mod replace`, Ginkgo label filter, 90m minimum suite timeout.
- **Add** `.github/scripts/e2e-prepare-env.sh`, `.github/scripts/e2e-prepare-workspace.sh`: helper scripts for secrets materialisation and self-hosted runner workspace cleanup.
- **Add** `docs/CI.md`: documents the reusable workflow design, inputs, secrets, and run-tests flow.
- **Update** `README.md`: add CI section linking to `docs/CI.md`.
- **Update** `.github/workflows/e2e-reusable.yml`: add `noop` pipeline_mode (all jobs echo mocked, no real steps run); add `test_suite` input (default `TestSdsNodeConfigurator`) to decouple hardcoded suite name from workflow.
- **Add** `.github/workflows/e2e-self-test.yml`: self-test caller that triggers the reusable workflow in `noop` mode on PRs touching CI files.
- **Update** `.github/workflows/e2e-reusable.yml`: add `skip_storage_e2e_replace` boolean input; gate `checkout storage-e2e`, `go mod edit -replace`, and `setup-go` (with dual-path cache) on this flag so storage-e2e can call the workflow without circular self-reference.
- **Update** `.github/workflows/e2e-self-test.yml`: set `skip_storage_e2e_replace: true`, `test_package: ./tests/test-template/`, `test_suite: TestTemplate`.
---

## 2026-06-23

- **Add** `gitleaks.toml`: content-based allowlist (`[extend] useDefault=true` + `regexTarget="line"` regex for `dXNlcjp0b2tlbg==`) for the base64 test fixture in `internal/kubernetes/commander/client_test.go`. Replaces the commit-pinned `.gitleaksignore` fingerprint, which broke after rebasing `unit-tests` onto `main` (the introducing commit's SHA changed `5f1edc2`→`35e9bc7`). The regex allowlist survives history rewrites.
- **Bugfix** lint fixes in unit-test files surfaced by `main`'s golangci-lint config (after rebase): `pkg/retry/retry_test.go` (gocritic paramTypeCombine on `statusErr`, `cancelled`→`canceled` misspellings), `internal/kubernetes/commander/client_http_test.go` (`behaviour`→`behavior`, gofmt), `pkg/testkit/stress_tests_test.go` (gofmt), `pkg/kubernetes/apply_test.go` (dropped ineffectual `got` assignment in `FindUnsetEnvVars` test), `pkg/cluster/vms_test.go` (staticcheck QF1001 De Morgan simplification).

---

## 2026-06-24

- **Remove** `.github/workflows/unit-tests.yml` per PR #20 review: `main`'s `.github/workflows/go-checks.yml` already runs lint + race-enabled unit tests + coverage publishing, so the dedicated workflow was a duplicate. Updated the `Makefile` header comment to point at `go-checks.yml` instead of the removed workflow.
- **Update** `.github/workflows/e2e-reusable.yml`: replace mocked `create-cluster`/`teardown-cluster` jobs with real steps that check out the repo (+ storage-e2e when `skip_storage_e2e_replace=false`), set up Go, materialize `E2E_DVP_BASE_CLUSTER_*` SSH key/kubeconfig to temp files, and run `go run ./cmd/bootstrap-cluster` / `go run ./cmd/remove-cluster` with the DVP provider env. `noop` mode still echoes.

---

## 2026-06-25

- **Add** `docs/superpowers/specs/2026-06-25-e2e-github-actions-ci-design.md`: design for a from-scratch reusable GitHub Actions e2e CI (resolve → bootstrap → run-tests → teardown), PR-label driven (`e2e/run` gate, `e2e/keep-cluster` skips teardown, `e2e/label:*` → Ginkgo filter), stable per-PR namespace, with a caller template for consumer modules.
- **Add** `.github/scripts/e2e-resolve-labels.sh` + test: PR labels → keep_cluster/ginkgo_filter/namespace outputs.
- **Add** `.github/scripts/e2e-prepare-creds.sh` + test: SSH key/kubeconfig secrets → temp files, workspace prune.
- **Add** `.github/scripts/e2e-run-tests.sh` + test: go mod replace (self-aware) + go test with Ginkgo label filter.
- **Add** `.github/workflows/e2e.yml` reusable workflow: resolve → bootstrap → run-tests → teardown, label-gated, stable per-PR namespace.
- **Add** `.github/templates/e2e-tests.yml` caller template for consumer modules.
- **Remove** old CI (`e2e-reusable.yml`, `e2e-prepare-env.sh`, `e2e-prepare-workspace.sh`); **Refactor** `e2e-self-test.yml` into a thin caller of `e2e.yml`.
- **Update** `e2e-self-test.yml`: pin `storage_e2e_ref` to PR head SHA so self-test dogfoods PR scripts/cmd.
- **Update** docs for the new CI: rewrote `docs/CI.md`, README CI section, ARCHITECTURE `.github/` + `cmd/` tree.
- **Update** `docs/CI.md`: note that `test_timeout` is reserved and not yet consumed by the skeleton run-tests.
- **Update** `e2e.yml`: clarify `test_timeout` input description as reserved/not-yet-wired.
- **Update** `.github/workflows/e2e.yml`: bump actions to latest (checkout v4→v7, setup-go v5→v6, upload-artifact v4→v7)
  and set `cache: false` on Setup Go (self-hosted runner caches Go in `runner.temp`, so setup-go's post-job cache save
  was slow and targeted the wrong dir).
- **Add** `e2e/` as a separate Go module (`github.com/deckhouse/storage-e2e/e2e`) with a Ginkgo suite (
  `e2e_suite_test.go`) and four label-tagged smoke specs (`e2e_test.go`: smoke/integration/regress/stress-test) to
  exercise CI label routing; added `./e2e` to `go.work`. Does not use the (old) `pkg/storage-e2e` SDK yet; the cluster
  is provisioned by the CI bootstrap job.
- **Update** `.github/workflows/e2e-self-test.yml`: point self-test at the new `e2e` module (`module_path: e2e`,
  `test_package: ./`, `cluster_config: e2e/cluster_config.yml`), so it now also exercises the `go mod replace` path.
  Removed stray duplicate `e2e/tests/cluster_config.yaml`.
- **Refactor** drop `go.work`/`third_party/deckhouse` workspace hack for IDE module resolution: added
  `hack/deckhouse-stub`
  (empty module) and a `replace` block in `go.mod` pointing the 6 unpublished `github.com/deckhouse/deckhouse/*`
  submodules (dhctl, egress-gateway-agent, go_lib/cloud-data, go_lib/dependency/{k8s/drain,vsphere}, go_lib/registry) at
  the stub, so `go list -m all` (IDE indexing) resolves without a local deckhouse clone. Builds/tests still use the real
  deckhouse module from the proxy.
- **Bugfix** `.github/workflows/e2e.yml`: fix `cluster_config not found` on self-hosted runners. The `resolve`
  job's non-cone sparse checkout of `.github/scripts` polluted the shared workspace-root `.git`
  (`core.sparseCheckout=true` persists, actions/checkout#2249), so `bootstrap`'s `clean: false` re-checkout only
  restored `.github/scripts` and dropped `e2e/cluster_config.yml`. Moved `resolve` to a dedicated `_se2e-scripts`
  path with cone mode (no root pollution) and added a "Reset stale sparse-checkout" step before the module checkout
  in bootstrap/run-tests/teardown so already-polluted runners self-heal.
- **Bugfix** restore the `hack/deckhouse-stub` `go.mod` and `replace` block in `go.mod` that were dropped during the
  `deckhouse v1.74.0 → v1.76.0` bump (GoLand `go list -m -u all` failed on unpublished submodules); added the new
  `go_lib/configtools/conversion` submodule introduced in v1.76.0 to the replace block.
- **Update** `.github/scripts/e2e-run-tests.sh`: in the consumer-module branch, back up `go.mod`/`go.sum` and restore
  them via a `trap ... EXIT` (preserving `$?`) so the persistent `clean: false` workspace is not left with a stale
  absolute-path replace, and replaced `go mod tidy` with `go mod download` for deterministic, network-light runs;
  extended `.github/scripts/tests/test-run-tests.sh` to assert `mod download` (not `mod tidy`) and that `go.mod` is
  restored after the run.
- **Add** `docs/superpowers/specs/2026-06-25-cluster-tool-images-design.md`: design for containerizing
  `cmd/bootstrap-cluster` + `cmd/remove-cluster` into a single versioned `cluster-tool` image (dev-registry, semver on
  release tags), consumed via an optional `cluster_tool_image` input in `e2e.yml` (image path for consumers, `go run`
  kept for self-test); no `go:embed` refactor since the legacy `PrepareBootstrapConfig` template path is not on the
  image's DVP bootstrap path.

---

## 2026-06-26

- **Bugfix** `.github/workflows/e2e.yml`: checkout the caller module repo before bootstrap/teardown so
  `E2E_CLUSTER_CONFIG_YAML_PATH` points at an existing `cluster_config.yml`; format cluster CLI fatal errors with
  `log.Fatalf` instead of glued key/value arguments.
- **Bugfix** `.github/workflows/e2e.yml`: checkout `storage-e2e` into `_storage-e2e` in the `run-tests` job before
  invoking `.github/scripts/e2e-run-tests.sh`.
- **Bugfix** `internal/infrastructure/ssh/v2/conn.go` (`newConn`): derive keepalive context via
  `context.WithCancel(context.WithoutCancel(ctx))` instead of `context.Background()` to satisfy `contextcheck` while
  keeping the loop lifetime tied to the connection (still cancelled in `Close`).
- **Refactor** `internal/infrastructure/ssh/v2/{options.go,endpoint.go}`: centralise `ssh.InsecureIgnoreHostKey` into a
  single documented `insecureIgnoreHostKey` helper with one `//nolint:gosec` (G106); call sites now use the wrapper.

## 2026-06-29

- **Add** `docs/REVIEW_SSH_V2.md`: code-review report for `internal/infrastructure/ssh/v2` (PR #27) with a fix
  checklist (1 High, 3 Medium, 1 Low, 4 Nit) and per-finding explanations.
- **Bugfix** `internal/infrastructure/ssh/v2/conn.go` (review #1): add a connection-lifetime `lifeCtx`/`lifeCancel`
  (cancelled in `Close`) and back reconnect dials with it instead of `context.WithoutCancel(ctx)`, so `Close()` aborts
  an in-flight keepalive reconnect immediately rather than blocking on `dialTimeout`; `refresh` no longer takes a
  `ctx` param. Added regression test `TestConnCloseAbortsInFlightReconnect` and made the test dialer gate honour ctx.
- **Update** `internal/infrastructure/ssh/v2/{options.go,conn.go}` (review #2): decouple the keepalive probe timeout
  from the probe interval via new `WithKeepaliveTimeout` option and `resolveKeepaliveTimeout` helper (default
  `min(interval, 10s)`); `keepaliveLoop` now takes an explicit `probeTimeout`. Added `options_test.go`.
- **Refactor** `internal/infrastructure/ssh/v2/{endpoint.go,options.go,client.go}` (review #3): make host key
  resolution single-source — remove the unreachable `insecureIgnoreHostKey` fallback in `clientConfig` and document in
  `WithHostKeyCallback` that the default only reaches `Route`-built dialers, not custom `Dialer`s.
- **Update** `internal/infrastructure/ssh/v2/{options.go,client.go}` (review #4): track an `insecureHostKey` flag on
  options, document the insecure-by-default host key policy in the package godoc, and log a `Warn` from `New` when host
  key verification is disabled.
- **Bugfix** `internal/infrastructure/ssh/v2/conn.go`: fix golangci-lint findings — `misspell` ("cancelled" → "
  canceled")
  and `contextcheck` (add `//nolint` for the deliberate `context.AfterFunc(c.lifeCtx, …)` that uses the conn lifetime
  rather than the per-caller ctx).
- **Refactor** `internal/infrastructure/ssh/v2/endpoint.go`: replace `Endpoint.KeyPath` with `KeyData []byte`; delete
  `expandTilde` and the file-read branch in `clientConfig` so the transport layer never reads files or expands paths.
  Updated `endpoint_test.go` with `KeyData` signer cases (incl. passphrase-protected). [Possible compatibility break]
- **Refactor** `internal/provisioning/dvp/config.go`: make `Config` struct path/content pair-based, add `LoadConfig`
  (injectable env map), accumulating `Validate` with sentinel errors via `errors.Join`, `Resolve`→`Credentials`
  (single path-expansion/read site), rename `HasJumpHost`→`JumpHostConfigured`; moved `expandUserPath` here. Added
  `config_test.go` + `credentials_test.go`.
- **Refactor** `internal/provisioning/dvp/kubeconfig.go`: replace `readKubeconfig` with free function
  `buildRestConfig([]byte, string)`; drop `expandUserPath` (moved to `config.go`). Added `kubeconfig_test.go`.
- **Refactor** `internal/provisioning/dvp/provider.go`: split thin `NewDVPProvider` (reads env, resolves creds) from
  injectable `newProvider`; `buildSSHClient` passes `KeyData` from resolved credentials; log a derived
  `path`/`inline` kubeconfig source.
- **Update** `docs/ARCHITECTURE.md`: document `Endpoint.KeyData` and add the DVP base-cluster env-var reference
  (path/content pairs) to Section 7.
- **Refactor** CI: drop the temp-file credential workaround now that `dvp.Config` accepts inline content. Renamed
  `.github/scripts/e2e-prepare-creds.sh` → `e2e-prune-workspace.sh` (workspace prune only); `.github/workflows/e2e.yml`
  now passes `E2E_DVP_BASE_CLUSTER_SSH_PRIVATE_KEY`/`..._SSH_JUMP_PRIVATE_KEY` as inline content and decodes the
  base64 `..._KUBECONFIG` secret into `..._KUBECONFIG` content inline before `go run` (bootstrap + teardown); removed
  the "Cleanup temp credentials" steps. Replaced `tests/test-prepare-creds.sh` with `tests/test-prune-workspace.sh`.
  Updated `docs/CI.md`.
- **Remove** `.github/workflows/e2e.yml`: drop the jump-host env wiring (`..._SSH_JUMP_HOST/_USER/_PRIVATE_KEY`) from
  bootstrap + teardown — the jump host was never actually used in CI (direct connection), and the new all-or-nothing
  jump validation would fail on a partial config. Left a comment on how to re-enable (set all three together).
- **Bugfix** `pkg/kubernetes/modules.go`: `WaitForModuleReady` now derives a `context.WithTimeout(ctx, timeout)` instead
  of only logging the timeout value; previously the `timeout` arg was never enforced so the wait hung until the parent
  context was canceled (e.g. "waiting for virtualization module" never timing out at 1m).
- **Add** Commander cluster provider for the new provider abstraction:
  `internal/provisioning/commander/{config.go,provider.go,provider_test.go}`. `Bootstrap`
  creates a cluster from a Commander template (resolving template version + optional registry,
  merging `E2E_COMMANDER_VALUES` and a forced `prefix`) and waits for Ready (idempotent: reuses an
  existing cluster of the same name); `Remove` deletes it (tolerates `ErrClusterNotFound`). Config is
  env-driven via the `E2E_COMMANDER_*` prefix. Reuses the existing `internal/kubernetes/commander`
  API client. The cluster name is taken verbatim from config (no randomization) so the separate
  `cmd/bootstrap-cluster` and `cmd/remove-cluster` processes act on the same cluster.
- **Update** `pkg/clusterprovider/registry/registry.go`: seed `ModeCommander` →
  `commander.NewCommanderProvider`. Adjusted `registry_test.go` (`TestRegistryGet_UnregisteredMode`
  now uses a bogus mode; `TestDefaultRegistry_HasBuiltinProviders` asserts both `dvp` and `commander`).
- **Update** `.github/workflows/e2e.yml`: add a `cluster_provider` input (`dvp` default | `commander`),
  thread it into `E2E_TEST_CLUSTER_PROVIDER` for bootstrap/teardown, gate the DVP-only `Prepare
  credentials` step on `cluster_provider == 'dvp'`, and pass the `E2E_COMMANDER_*` env (typed/defaulted
  fields via `|| <default>` so unset vars never override Go-side defaults). `.github/templates/e2e-tests.yml`
  and `docs/CI.md` document the provider choice and the Commander secrets/vars.
- **Merge** reconcile `origin/main` (Commander provider, #31) with the DVP config/CI refactor in
  `.github/workflows/e2e.yml` and `docs/CI.md`: the DVP-only `Prepare credentials` step is replaced by the
  provider-agnostic `Prune stale workspace caches` step (credentials flow as inline content), while the Commander
  `cluster_provider` input and `E2E_COMMANDER_*` env are kept; the base64 kubeconfig is decoded inline in both
  bootstrap and teardown.
- **Add** `internal/provisioning/dvp/provider.go`: explicit API-server connectivity check in `Bootstrap`
  (calls `kubernetes.NewClientsetWithRetry` right after `buildRestConfig`) so a dead SSH tunnel / bad
  kubeconfig fails fast with a clear error instead of timing out inside `WaitForModuleReady`.
