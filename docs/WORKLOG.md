# Work Log

All notable changes to this repository are documented here. New entries are appended with date-time.

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

## 2026-05-05

- **Add** `internal/config/overrides.go` + `_test.go`: `ExpandEnvInModulePullOverride` resolves `${VAR}` placeholders in `modulePullOverride` at config load time; missing env fails fast with an explicit error so CI can point modules at `pr<N>` / `mr<N>` images via a single env var (`MODULE_IMAGE_TAG`) without editing `cluster_config.yml`.
- **Update** `internal/cluster/cluster.go::LoadClusterConfig` and `pkg/cluster/cluster.go::loadClusterConfigFromPath`: hook `ExpandEnvInModulePullOverride` right after `yaml.Unmarshal`.
- **Update** `README.md`: documented `${VAR}` form in `modulePullOverride` and the fail-fast behavior on unset env vars.
- **Refactor** `internal/config/env.go`: extracted `ApplyDefaults()` out of `ValidateEnvironment` so suites that don't call validation still get defaults for `SSH_VM_USER` / `SSH_PRIVATE_KEY` / `SSH_PUBLIC_KEY` / `TEST_CLUSTER_NAMESPACE` / `YAML_CONFIG_FILENAME` / `TEST_CLUSTER_CLEANUP`.
- **Update** `pkg/cluster/cluster.go::CreateTestCluster`: call `config.ApplyDefaults()` defensively + fall back to `config.YAMLConfigFilenameDefaultValue` when the filename arg is empty.
- **Update** `internal/cluster/cluster.go::GetKubeconfig`: added a third-tier fallback to `clientcmd.NewDefaultClientConfigLoadingRules` (KUBECONFIG / `~/.kube/config`) + `MinifyConfig` when SSH retrieval fails and `KUBE_CONFIG_PATH` is unset, so a developer whose local `kubectl` already targets the base cluster doesn't have to set anything.
- **Bugfix** `pkg/cluster/setup.go::executeDhctlBootstrap`: pass `FORCE_NO_PRIVATE_KEYS=true` and `USE_AGENT_WITH_NO_PRIVATE_KEYS=true` env vars into the `dhctl bootstrap` container so `lib-connection` stops opening `/root/.ssh/id_rsa` and authenticates exclusively via the mounted ssh-agent socket — fixes "Failed to read private keys from flags" on passphrase-protected keys.
- **Bugfix** `pkg/cluster/vms.go::generateCloudInitUserData`: pin apt to `mirror.yandex.ru` and force IPv4 (`Acquire::ForceIPv4=true`) in cloud-init, so `package_update` and Docker install stop stalling when `archive.ubuntu.com` IPs are partially unreachable.
- **Refactor** `internal/infrastructure/ssh/client.go::StartTunnel` (both `*client` and `*jumpHostClient`): extracted shared `runTunnelLoop` + `tunnelDialer`. On dial failure that looks like a dropped SSH session, the loop now logs a visible WARN, calls the existing `reconnect()` (retry + exponential backoff), and retries the dial once with the freshly rebuilt session. Fixes the "test hangs 20 minutes silently after Wi-Fi flap" failure mode.
- **Add** `pkg/kubernetes/poll.go`: `pollResourceUntilReady` centralizes the `WaitFor*Ready` loops with a per-call `PollGetTimeout` (30s) on every Get and WARN logging once consecutive Get failures cross 3, so a dropped tunnel surfaces in seconds instead of after the 20-minute readyTimeout.
- **Refactor** `pkg/kubernetes/cephcluster.go`, `pkg/kubernetes/cephblockpool.go`, `pkg/kubernetes/cephfilesystem.go`: `WaitForCephClusterReady` / `WaitForCephBlockPoolReady` / `WaitForCephFilesystemReady` migrated to `pollResourceUntilReady`. Public signatures unchanged.
- **Update** `docs/FUNCTIONS_GLOSSARY.md`: noted that the three `WaitForCeph*Ready` helpers now apply a per-call deadline and emit WARN on consecutive Get failures.
- **Update** `docs/ARCHITECTURE.md`: added `pkg/kubernetes/poll.go` to Section 1.1 and Section 3.6, added `pkg/kubernetes/cephfilesystem.go` (carry-over from the prior commit), added `internal/config/overrides.go` to Section 3.1.
