# Worklog

## 2026-07-09 (drop DiskManager capability)

- Removed the `DiskManager` capability from the branch to shrink the change set (deferred as a follow-up task, tracked in `TODO.md`). Deleted `pkg/clusterprovider/disks.go`, `internal/provisioning/dvp/disks.go` + `disks_test.go`; dropped the `Disks DiskManager` field from `clusterprovider.Cluster`, the `Disks` literal from DVP `ConnectTestCluster`, the `Disks()` accessor + nil-check from `pkg/e2e`, the `DiskManager`/`DiskSpec`/`Disk`/`DiskRef` aliases, `VerifyDiskManager` + disk config from `pkg/e2e/conformance`, and the disk methods from the `virtClient` seam + fake. [Possible compatibility break] — `clusterprovider.Cluster.Disks`, `e2e.Cluster.Disks()` and the `Disk*` types are gone (feature branch only, unreleased). The pre-existing `pkg/kubernetes/virtualdisk.go` and `internal/kubernetes/virtualization` VirtualDisk/VMBDA clients (used by VM provisioning) are untouched.

## 2026-07-06 (SRP split)

- Split the connect contract and DVP connect helpers by domain (no API/behavior changes): `pkg/clusterprovider/cluster.go` now holds only the `Cluster` aggregate + `ErrConnectUnsupported`, node exec types moved to `nodeexec.go`, disk types to `disks.go`; on the DVP side `vmIPResolver` and `dvpNodeExecutor` moved out of `connect_test_cluster.go` into `vm_ip_resolver.go` and `node_executor.go`.

## 2026-07-06 (cleanup)

- Swept dead leftovers from earlier iterations out of the uncommitted change set: staged the deletion of abandoned files (`internal/kubernetes/clusterlock/lock.go`, `internal/provisioning/commander/{session,disks,disks_test}.go` — created and superseded within the same work), removed the empty `managedLabels()` wrapper in `internal/provisioning/dvp/vm/labels.go` (callers use `ManagedLabels()` directly), and dropped the unused `clusterlock.GetLeaseInfo`/`LeaseInfo` (the held-lease error now reads the Lease fields directly).

## 2026-07-06 (later)

- Renamed the SDK connection contract (`pkg/clusterprovider`, no external implementers yet): `Session` → `Cluster` (`session.go` → `cluster.go`), `Provider.OpenSession` → `Provider.ConnectTestCluster`, `ErrSessionUnsupported` → `ErrConnectUnsupported` (re-exported as `e2e.ErrConnectUnsupported`). The legacy `Connector` interface is untouched; dvp (`session.go` → `connect_test_cluster.go`), the commander stub and test fakes renamed accordingly.
- Deduplicated the provider connection plumbing into `internal/kubernetes/kubeaccess`: `FetchKubeconfig` (super-admin.conf || admin.conf over SSH), `RewriteServer`, `BuildRestConfig` / `BuildRestConfigDirect` (with the shared tunnel transport timeouts) and `TunnelRestConfig` (open tunnel + rest.Config at its local end). `dvp/kubeconfig.go` now only derives SSH public keys; `commander/kubeconfig.go` deleted; both connectors call kubeaccess.
- DVP base-cluster connect now auto-detects direct API access: `kubeaccess.DirectReachable` probes `/version` (5s budget, no retries) with a rest.Config built straight from the provided kubeconfig; when reachable the SSH tunnel is skipped (no new env vars). Unit tests: kubeaccess (moved from `dvp/kubeconfig_test.go` + `FetchKubeconfig`/`DirectReachable` via httptest) and the dvp direct-connect path.

## 2026-07-06

- Merged `SessionProvider` into `Provider`: `OpenSession(ctx) (*Session, error)` is now a required `Provider` method (the library has no external implementers yet, so no compatibility impact). Commander's `OpenSession` is an explicit not-implemented stub returning `clusterprovider.ErrSessionUnsupported` (sentinel moved to `pkg/clusterprovider`, re-exported as `e2e.ErrSessionUnsupported`); `e2e.Connect` calls `OpenSession` directly instead of a type assertion.
- Replaced the SDK cluster lock with a `coordination.k8s.io/v1` Lease (`internal/kubernetes/clusterlock/lease.go`): `e2e.Connect` acquires the `e2e-cluster-lock` Lease in `default` (holder metadata in annotations), renews it in the background (duration 5m, tick 100s) and `Cluster.Close` releases it only if still the holder (UID precondition). A lease left behind by a dead run self-expires and is taken over. Unit tests on `client-go` fakes.
- Marked the ConfigMap-based cluster lock in `pkg/cluster/lock.go` as `Deprecated` (comments only — implementation and behavior untouched, kept for the legacy `pkg/cluster` flow).
- Docs: updated `ARCHITECTURE.md` (clusterlock tree, locking mechanism section, troubleshooting) and `FUNCTIONS_GLOSSARY.md` (Cluster Lock section deprecated, `Connect` description).

## 2026-07-03

- Added the `pkg/e2e` SDK: `e2e.Connect(ctx, opts...)` attaches a suite to the provider-managed cluster (provider from `E2E_TEST_CLUSTER_PROVIDER` via the registry, session open, health check, cluster lock) and returns a `*Cluster` handle with `RESTConfig`/`Clientset`/`Dynamic`, `Nodes()` (`NodeExecutor`) and `Disks()` (`DiskManager`); `Close` releases the lock and the session.
- Added capability contracts to `pkg/clusterprovider` (`session.go`): `Session`, `NodeExecutor`, `DiskManager`, `ExecResult`, `DiskSpec`/`Disk`/`DiskRef`; sessions are opened via `Provider.OpenSession`. The legacy `Connector` interface remains untouched.
- DVP provider implements `OpenSession` (`internal/provisioning/dvp/session.go`, `disks.go`): master resolved by the first master's VM IP on the base cluster; `NodeExecutor` runs commands over SSH through the base endpoints; `DiskManager` creates labeled (`storage-e2e.deckhouse.io/managed-by`) `VirtualDisk` + `VirtualMachineBlockDeviceAttachment` and waits for the Attached phase, so `Provider.Remove` sweeps leftovers. Exported the managed-by label constants from `internal/provisioning/dvp/vm`.
- Commander session support is deliberately out of scope (separate task): the SDK currently supports the `dvp` provider only; `e2e.Connect` on commander fails with `ErrSessionUnsupported`.
- Added `pkg/e2e/conformance`: `Verify` / `VerifyNodeExecutor` / `VerifyDiskManager` contract checks every provider must pass against a live cluster (exit-code semantics, sudo, attach-visibility, detach idempotency).
- Marked `pkg/cluster` as `Deprecated` (comment only; kept for backward compatibility, nothing removed). `tests/test-template` intentionally stays on the legacy API for now.
- Docs: updated `ARCHITECTURE.md` (package tree, public API, provider/SDK env vars) and `FUNCTIONS_GLOSSARY.md` (pkg/e2e, conformance, clusterprovider contracts).

## 2026-07-02

- Added `kubernetes.DeleteNamespace(ctx, config, name)` (idempotent, waits for full removal) and wired it into the DVP provider's `Remove` so the test namespace is deleted after VM teardown instead of being left behind. Extended the `kubeOps` seam with `DeleteNamespace` and its fake.
