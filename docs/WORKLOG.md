# Work Log

All notable changes to this repository are documented here. New entries are appended with date-time.

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
