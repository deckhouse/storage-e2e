# Work Log

All notable changes to this repository are documented here. New entries are appended with date-time.

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

## 2026-06-08

- **Add** `pkg/config/config_test.go`: unit tests for `config.New` covering provider parsing, missing required `TEST_CLUSTER_PROVIDER` (error), empty-value handling, and table-driven provider values.

---

## 2026-06-19

- **Bugfix** `internal/config.ResolveModulePullOverrides`: detect malformed `${...}` on the original string (stripping
  valid refs first) instead of the resolved value, avoiding a false "malformed" error when an env value itself contains
  `${...}`.
