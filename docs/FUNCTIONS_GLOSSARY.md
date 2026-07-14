# Functions Glossary

All exported functions available in the `pkg/` directory, grouped by resource.

## Table of Contents

- [E2E SDK (pkg/e2e)](#e2e-sdk-pkge2e)
- [Provider Conformance (pkg/e2e/conformance)](#provider-conformance-pkge2econformance)
- [Cluster Provider Contracts (pkg/clusterprovider)](#cluster-provider-contracts-pkgclusterprovider)
- [Cluster](#cluster)
- [Commander Operations (pkg/commander)](#commander-operations-pkgcommander)
- [Cluster Lock](#cluster-lock)
- [VM (Virtual Machine)](#vm-virtual-machine)
- [Setup / Bootstrap](#setup--bootstrap)
- [Kubernetes Client](#kubernetes-client)
- [Storage E2E](#storage-e2e)
- [YAML Apply](#yaml-apply)
- [Namespace](#namespace)
- [Nodes](#nodes)
- [NodeGroup](#nodegroup)
- [Pod](#pod)
- [PVC (PersistentVolumeClaim)](#pvc-persistentvolumeclaim)
- [StorageClass](#storageclass)
- [VolumeSnapshotClass](#volumesnapshotclass)
- [BlockDevice](#blockdevice)
- [LVMVolumeGroup](#lvmvolumegroup)
- [LocalStorageClass](#localstorageclass)
- [VirtualDisk](#virtualdisk)
- [VM Pod](#vm-pod)
- [Secrets](#secrets)
- [Modules](#modules)
- [Retry](#retry)
- [Rook Config Override](#rook-config-override)
- [CephFilesystem (Rook)](#cephfilesystem-rook)
- [ElasticCluster / ElasticStorageClass (sds-elastic)](#elasticcluster--elasticstorageclass-sds-elastic)
- [Rook verifiers (sds-elastic renamed group)](#rook-verifiers-sds-elastic-renamed-group)
- [Default StorageClass (Testkit)](#default-storageclass-testkit)
- [Elastic (Testkit)](#elastic-testkit)
- [Stress Tests (Testkit)](#stress-tests-testkit)
- [Ceph CRC (Testkit)](#ceph-crc-testkit)

---

## E2E SDK (pkg/e2e)

`pkg/e2e/e2e.go`, `pkg/e2e/cluster.go`

The SDK entry point for test suites: attach to a provider-managed cluster (bootstrapped by `cmd/bootstrap-cluster`) and
use provider-supplied capability strategies without mode-specific branching.

- `Connect(ctx, opts...)` — Attaches the test run to the provider-managed cluster: reads `E2E_TEST_CLUSTER_PROVIDER`
  from env, builds the provider via the registry, calls `Provider.ConnectTestCluster` (API access + strategies), waits
  for the cluster to be healthy and acquires the cluster lock (a `coordination.k8s.io/v1` Lease renewed in the
  background; a stale lock from a dead run self-expires). Returns a `*Cluster` handle.
- `WithTestName(name)` — Connect option: sets the test name recorded in the cluster lock (defaults to the test binary
  name).
- `WithoutLock()` — Connect option: skips acquiring the cluster lock.
- `WithoutHealthCheck()` — Connect option: skips the post-connect cluster health check.
- `WithHealthCheckTimeout(d)` — Connect option: overrides the health check wait budget (default 10m).
- `(*Cluster) ProviderName()` — Reports which provider manages the cluster (`dvp`, `commander`).
- `(*Cluster) RESTConfig()` — Returns the `*rest.Config` pointed at the test cluster's API server; pass to
  `pkg/kubernetes` / `pkg/testkit` helpers.
- `(*Cluster) Clientset()` — Returns a cached typed Kubernetes client.
- `(*Cluster) Dynamic()` — Returns a cached dynamic Kubernetes client.
- `(*Cluster) Nodes()` — Returns the provider's `NodeExecutor` (run commands on cluster nodes).
- `(*Cluster) Disks()` — Returns the provider's `DiskManager` (create/delete/attach/detach additional node disks).
  When the provider does not support disk management (commander, for now), every operation on the returned manager
  fails with `ErrDisksUnsupported`.
- `(*Cluster) Close(ctx)` — Releases the cluster lock and the provider connection (SSH clients, tunnels). Idempotent.

Type aliases `NodeExecutor`, `ExecResult`, `DiskManager`, `DiskSpec`, `Disk` re-export the contracts from
`pkg/clusterprovider` so suites only need the `e2e` import; `ErrDisksUnsupported` is re-exported as
`e2e.ErrDisksUnsupported`.

## Provider Conformance (pkg/e2e/conformance)

`pkg/e2e/conformance/conformance.go`

Contract checks every provider must pass against a live cluster (run explicitly; they exercise real infrastructure).

- `Verify(ctx, cluster, cfg)` — Runs all conformance checks against a connected `*e2e.Cluster` and returns a per-check
  `*Report`; picks the first worker node when `cfg.NodeName` is empty.
- `VerifyNodeExecutor(ctx, nodes, nodeName)` — Checks the `NodeExecutor` contract: stdout/stderr captured separately,
  non-zero exit codes reported without error, passwordless sudo available.
- `(*Report) Err()` — Joins the errors of all failed checks (nil when everything passed).

## Cluster Provider Contracts (pkg/clusterprovider)

`pkg/clusterprovider/provider.go`, `pkg/clusterprovider/cluster.go`, `pkg/clusterprovider/disks.go`,
`pkg/clusterprovider/config.go`, `pkg/clusterprovider/mode.go`

- `Provider` (interface: `Name`, `Bootstrap`, `Remove`, `ConnectTestCluster`) — Provisions and removes a test cluster
  for a specific backend (`cmd/bootstrap-cluster` / `cmd/remove-cluster`) and attaches test runs to it:
  `ConnectTestCluster` returns a `*Cluster` (rest.Config + `NodeExecutor` + cleanup) or
  `ErrConnectUnsupported` when the provider cannot connect test runs yet (commander).
- `ErrConnectUnsupported` — Sentinel returned by `Provider.ConnectTestCluster` when the provider does not support
  connecting test runs; re-exported as `e2e.ErrConnectUnsupported`.
- `Connector` (interface: `Connect`) — Optional legacy capability returning a bare `*rest.Config` + cleanup; superseded
  by `Provider.ConnectTestCluster` for the SDK path.
- `NodeExecutor` (interface: `Exec(ctx, nodeName, command)`) — Runs commands on cluster nodes; a completed command with
  a non-zero exit code is not an error (exit code is in `ExecResult.ExitCode`).
- `DiskManager` (interface: `CreateDisk`, `DeleteDisk`, `AttachDisk`, `DetachDisk`) — Manages additional block devices
  on cluster nodes; all operations block until the target state is reached (deadline via ctx). DVP implements it with
  `VirtualDisk`/`VirtualMachineBlockDeviceAttachment` resources on the base cluster; providers that do not support it
  leave `Cluster.Disks` nil and the `e2e` facade substitutes an `ErrDisksUnsupported` stub. Encapsulates the
  provider/base-cluster choice that the lower-level `AttachVirtualDiskToVM`/`DetachAndDeleteVirtualDisk` helpers in
  `pkg/kubernetes` leave to the caller.
- `DiskSpec` (struct: `Name`, `Size`, `StorageClass`) — Input for `DiskManager.CreateDisk`; empty `StorageClass` means
  the provider default.
- `Disk` (struct: `Name`, `Size`, `StorageClass`, `Phase`) — Provider-managed disk description returned by
  `DiskManager.CreateDisk`.
- `ErrDisksUnsupported` — Sentinel returned by `DiskManager` operations when the provider does not support disk
  management; re-exported as `e2e.ErrDisksUnsupported`.
- `NewClusterConfig()` — Reads the provider-agnostic settings (`E2E_TEST_CLUSTER_PROVIDER`,
  `E2E_CLUSTER_CONFIG_YAML_PATH`) from the environment.

## Cluster

`pkg/cluster/cluster.go`

> **Deprecated:** the whole `pkg/cluster` package is legacy; new suites should use [pkg/e2e](#e2e-sdk-pkge2e).

- `CreateTestCluster(ctx, yamlConfigFilename)` — Creates a complete test cluster end-to-end: loads config, connects to base cluster, creates VMs, bootstraps Kubernetes, adds nodes, enables modules.
- `UseExistingCluster(ctx)` — Connects to an existing cluster, retrieves kubeconfig, and acquires a cluster lock. Supports jump host via `SSH_JUMP_HOST`.
- `CleanupExistingCluster(ctx, resources)` — Releases cluster lock and closes connections for an existing cluster.
- `UseCommanderCluster(ctx)` — Connects to or creates a cluster via Deckhouse Commander, waits for readiness, retrieves kubeconfig, and acquires lock.
- `CleanupCommanderCluster(ctx, resources)` — Releases Commander cluster resources and optionally deletes the cluster if it was created by us.
- `CreateOrConnectToTestCluster()` — High-level entry point that creates a new cluster or connects to an existing one based on configuration mode (existing / new / commander).
- `CleanupTestClusterResources(resources, testPassed...)` — Cleans up test cluster resources based on the mode used. Optionally cleans up stress namespaces if test passed.
- `CleanupTestCluster(ctx, resources)` — Cleans up all resources created by `CreateTestCluster`: stops tunnels, closes SSH, removes VMs.
- `WaitForTestClusterReady(ctx, resources)` — Waits for all modules in the test cluster to become Ready.
- `ConnectToCluster(ctx, opts)` — Establishes SSH connection to a cluster, retrieves kubeconfig, and sets up port forwarding tunnel.
- `CheckClusterHealth(ctx, kubeconfig, opts...)` — Checks deckhouse deployment health, bootstrap secrets availability, and webhook-handler readiness.
- `DefaultCheckClusterHealthOptions()` — Returns default health check options with all checks enabled.
- `WaitForWebhookHandler(ctx, kubeconfig, timeout)` — Waits for webhook-handler deployment to be ready and service endpoints registered.
- `GenerateRandomSuffix(length)` — Generates a random alphanumeric suffix of specified length.
- `OutputEnvironmentVariables()` — Outputs all environment variables to GinkgoWriter for debugging.
- `SetExtraCommanderValues(values)` — Sets additional values for Commander cluster creation (merged with `COMMANDER_VALUES` env var).
- `GetCommanderResources()` — Returns stored Commander cluster resources.
- `SetCommanderResources(res)` — Stores Commander cluster resources for later cleanup.
- `ClearCommanderResources()` — Clears stored Commander cluster resources.

## Commander Operations (pkg/commander)

`pkg/commander/mastercount.go`

Suite-facing operations against the Deckhouse Commander that provisioned the cluster. Env-driven (reuse the `E2E_COMMANDER_*` vars), so callable from both the legacy `pkg/cluster` flow and the [pkg/e2e](#e2e-sdk-pkge2e) SDK.

- `SetMasterCount(ctx, masterCount)` — Changes the Commander cluster's control-plane node count to `masterCount` (1 or 3), approving the disruptive change request Commander raises, and blocks until the cluster converges (`in_sync`). Use for master-count transition tests (e.g. 3→1→3).

## Cluster Lock

`pkg/cluster/lock.go`

> **Deprecated.** The ConfigMap-based lock is superseded by the `coordination.k8s.io/v1` Lease lock that `e2e.Connect`
> acquires automatically (renewed in the background, self-expires after a crash). These functions are kept for
> backward compatibility with the legacy `pkg/cluster` flow.

- `AcquireClusterLock(ctx, kubeconfig, testName)` — Deprecated. Creates a ConfigMap lock to indicate the cluster is
  busy. Returns error if already locked.
- `ReleaseClusterLock(ctx, kubeconfig)` — Deprecated. Removes the cluster lock ConfigMap. Safe to call if lock doesn't
  exist.
- `IsClusterLocked(ctx, kubeconfig)` — Deprecated. Checks if the cluster is currently locked by looking for the lock
  ConfigMap.
- `GetClusterLockInfo(ctx, kubeconfig)` — Deprecated. Retrieves information about the current cluster lock holder.
- `ForceReleaseClusterLock(ctx, kubeconfig)` — Deprecated. Forcefully removes the cluster lock. Use only when sure no
  other test is running.

## VM (Virtual Machine)

`pkg/cluster/vms.go`

- `CreateVirtualMachines(ctx, virtClient, clusterDef)` — Ensures configured `VirtualMachineClass` exists (auto-create from `generic` with Host CPU when missing; clears inherited `nodeSelector`/`tolerations`; keeps sizing policies), creates CVIs/VMs in parallel, handles name conflicts, returns VM names and resource tracking info.
- `RemoveAllVMs(ctx, resources)` — Forcefully stops and deletes VMs, virtual disks, and virtual images.
- `RemoveVM(ctx, virtClient, namespace, vmName)` — Removes a single VM and its associated VirtualDisks and ClusterVirtualImage (if unused).
- `GetSetupNode(clusterDef)` — Returns the setup (bootstrap) VM node from ClusterDefinition.
- `GetVMIPAddress(ctx, virtClient, namespace, vmName)` — Gets IP address of a VM by querying its status. **Deprecated:** use `GatherVMInfo` instead.
- `GatherVMInfo(ctx, virtClient, namespace, clusterDef, vmResources, opts)` — Gathers IP addresses for all VMs and fills them into ClusterDefinition in-place.
- `GetNodeIPAddress(clusterDef, hostname)` — Gets IP address for a node by hostname from ClusterDefinition.
- `CleanupSetupVM(ctx, resources)` — Deletes the setup VM and its resources. **Deprecated:** use `RemoveVM` instead.

## Setup / Bootstrap

`pkg/cluster/setup.go`

- `GetOSInfo(ctx, sshClient)` — Detects OS and kernel version on a remote host via SSH (reads `/etc/os-release` and `uname -r`).
- `WaitForSSHReady(ctx, baseSSHClient, targetIP)` — Polls port 22 reachability on a target VM through the base cluster SSH client. Call after VMs reach "Running" but before SSH connection.
- `WaitForDockerReady(ctx, sshClient)` — Waits for Docker to be ready on the setup node (installed via cloud-init).
- `PrepareBootstrapConfig(clusterDef)` — Generates bootstrap configuration file from a template, calculates internal network CIDR from VM IPs.
- `UploadBootstrapFiles(ctx, sshClient, privateKeyPath, configPath)` — Uploads private key and config.yml to the setup node.
- `BootstrapCluster(ctx, sshClient, clusterDef, configPath)` — Bootstraps a Kubernetes cluster via `dhctl bootstrap` in a Docker container on the setup node.
- `AddNodesToCluster(ctx, kubeconfig, clusterDef, baseSSHUser, baseSSHHost, sshKeyPath)` — Adds nodes to the cluster by running bootstrap scripts from secrets on each node via SSH.
- `WaitForAllNodesReady(ctx, kubeconfig, clusterDef, timeout)` — Waits for all expected nodes (masters + workers) to become Ready in parallel.
- `GetSSHPublicKeyContent()` — Returns SSH public key content as string. Reads from file path or returns inline content.

## Kubernetes Client

`pkg/kubernetes/client.go`

- `NewClientsetWithRetry(ctx, config)` — Creates a Kubernetes clientset with retry logic for transient network errors. Validates connection with server version check.
- `NewDynamicClientWithRetry(ctx, config)` — Creates a Kubernetes dynamic client with retry logic for transient network errors.
- `NewVirtualizationClient(ctx, config)` — Creates a virtualization API client for VirtualMachine/VirtualDisk and related resources.

## Storage E2E

`pkg/storage-e2e/setup.go`

- `Initialize()` — Initializes framework-level prerequisites: logger setup and environment validation.

## YAML Apply

`pkg/kubernetes/apply.go`

- `NewApplyClient(config)` — Creates an ApplyClient for applying YAML manifests with retry logic.
- `(*ApplyClient) ApplyYAML(ctx, yamlContent, namespace)` — Applies YAML manifest(s) to the cluster (supports multi-document YAML separated by `---`).
- `(*ApplyClient) CreateYAML(ctx, yamlContent, namespace)` — Creates resources from YAML manifest(s). Fails if resources already exist.
- `(*ApplyClient) CreateYAMLFromFileWithEnvvars(ctx, filePath, namespace)` — Reads YAML file, validates and substitutes environment variables, then creates resources.
- `FindUnsetEnvVars(content)` — Finds all `${VAR}` patterns in content and returns those that are not set in the environment.

## Namespace

`pkg/kubernetes/namespace.go`

- `CreateNamespaceIfNotExists(ctx, config, name)` — Creates a namespace if it doesn't exist, or returns the existing one.
- `DeleteNamespace(ctx, config, name)` — Deletes a namespace and blocks until it is fully removed (idempotent; a missing
  namespace is treated as success).

## Nodes

`pkg/kubernetes/nodes.go`

- `GetNodes(ctx, kubeconfig)` — Returns all nodes in the cluster as `[]corev1.Node`.
- `GetWorkerNodes(ctx, kubeconfig)` — Returns all worker nodes as `[]corev1.Node` (excludes nodes with `node-role.kubernetes.io/control-plane` or `master` labels). Uses `GetNodes` internally.
- `LabelNodes(ctx, kubeconfig, nodeNames, labelKey, labelValue)` — Adds a label to each of the specified nodes. Retries on optimistic concurrency conflicts.
- `GetNodeTaints(ctx, kubeconfig, nodeName)` — Returns the taints (`[]corev1.Taint`) of the named node.
- `IsNodeCordoned(ctx, kubeconfig, nodeName)` — Checks whether a node has NoSchedule or NoExecute taints that would prevent DaemonSet pods from scheduling. Uses `GetNodeTaints` internally.
- `WaitForNodesLabeled(ctx, kubeconfig, nodeNames, labelKey, labelValue)` — Waits for all specified nodes to have a given label with the expected value. Polls in parallel every 10 seconds.

## NodeGroup

`pkg/kubernetes/nodegroup.go`

- `CreateStaticNodeGroup(ctx, config, name)` — Creates a NodeGroup resource with Static nodeType.

## Pod

`pkg/kubernetes/pod.go`

- `WaitForAllPodsReadyInNamespace(ctx, kubeconfig, namespace, timeout)` — Waits for all pods in a namespace to be in Ready condition.
- `WaitForPodsStatus(ctx, clientset, namespace, labelSelector, status, expectedCount, maxAttempts, interval)` — Waits for pods matching a label selector to reach a specific status (Running, Completed, etc.).

`pkg/kubernetes/pod_exec.go`

- `ExecInPod(ctx, kubeconfig, namespace, pod, container, cmd) (stdout, stderr, err)` — Runs a command inside a container via the apiserver's `pods/exec` subresource (SPDY). Returns stdout and stderr separately; the container must ship every binary referenced by `cmd`. Use this when the container has a usable shell/userland.
- `ReadFileFromPod(ctx, kubeconfig, namespace, pod, container, path)` — `ExecInPod` + `cat <path>`. Convenience wrapper for non-distroless images.
- `ReadFileFromDistrolessPod(ctx, kubeconfig, namespace, pod, targetContainer, path, opts)` — Reads a file from a distroless / scratch container that ships no `cat`/`sh`/`tar`. Injects a short-lived ephemeral container (`opts.DebugImage` is required — use a minimal image with `cat` and `sleep` from your cluster registry) with `targetContainerName=targetContainer`, polls until it goes Running (`opts.StartupTimeout`, defaults to 60s), then `cat /proc/1/root<path>` — `/proc/1/root` is the kernel-exposed FS root of PID 1 in the target container, which the ephemeral container can see thanks to the shared PID namespace. Adding the ephemeral container goes through the dedicated `/pods/<name>/ephemeralcontainers` subresource, so existing containers and the pod sandbox are NOT restarted, `metadata.generation` is not bumped, and ReplicaSet/DaemonSet observation is unaffected — downstream rollout / `checksum/...` annotation assertions still see a clean signal. Caveat: ephemeral containers cannot be removed once added, but each call generates a unique name and the `sleep 60` command exits on its own; entries pile up in `pod.status.ephemeralContainerStatuses` until the next pod recycle. Internally a one-shot wrapper around `OpenDistrolessReader` + `(*DistrolessReader).ReadFile`.
- `OpenDistrolessReader(ctx, kubeconfig, namespace, pod, targetContainer, opts) (*DistrolessReader, error)` — Long-lived variant of `ReadFileFromDistrolessPod`: injects ONE ephemeral container (`opts.DebugImage` required; sleeps for `opts.SessionTTL`, defaults to `DefaultDistrolessSessionTTL` = 30 min) and returns a session that can serve arbitrarily many cheap reads. Use this for polling loops (e.g. `Eventually(...)` waiting for a file's content to flip) so the ephemeral-container cold start is paid once instead of per iteration.
- `(*DistrolessReader) ReadFile(ctx, path)` — `cat /proc/1/root<path>` against the pre-injected ephemeral container. Cheap — just a `pods/exec` round-trip; no apiserver mutations.
- `(*DistrolessReader) PodName()` — Name of the pod this reader is bound to. Used by callers that need to detect rollouts (the pod name changes when the workload-controller recycles the pod) and re-`OpenDistrolessReader` against the new pod.
- `(*DistrolessReader) EphemeralName()` — Auto-generated name of the injected ephemeral container, mostly for logs.

## PVC (PersistentVolumeClaim)

`pkg/kubernetes/pvc.go`

- `WaitForPVCsBound(ctx, clientset, namespace, labelSelector, expectedCount, maxAttempts, interval)` — Waits for PVCs matching a label selector to be in Bound state.
- `WaitForPVCsResized(ctx, clientset, namespace, pvcNames, targetSize, maxAttempts, interval)` — Waits for PVCs to be resized to the target size.
- `ResizeList(ctx, clientset, namespace, pvcNames, newSize)` — Resizes multiple PVCs to a new size in parallel.

## StorageClass

`pkg/kubernetes/storageclass.go`

- `WaitForStorageClasses(ctx, kubeconfig, storageClassNames, timeout)` — Waits for multiple storage classes to become available in parallel. Returns map of names to errors.
- `WaitForStorageClass(ctx, kubeconfig, storageClassName, timeout)` — Waits for a single storage class to become available.
- `GetDefaultStorageClassName(ctx, kubeconfig)` — Returns the name of the current default StorageClass (annotated with `storageclass.kubernetes.io/is-default-class=true`), or `""` if none exists.
- `GetStorageClass(ctx, kubeconfig, name)` — Returns the `*storagev1.StorageClass` with the given name, or `(nil, nil)` if it does not exist.
- `SetGlobalDefaultStorageClass(ctx, kubeconfig, storageClassName)` — Updates the "global" ModuleConfig to set `spec.settings.storageClass` to the given name, making it the cluster default.
- `CreateStorageClass(ctx, kubeconfig, cfg)` — Creates a `storage.k8s.io/v1 StorageClass` directly from `StorageClassCreateConfig` (`Name`, `Provisioner`, `Parameters`, `VolumeBindingMode`, `ReclaimPolicy`, `AllowExpansion`, `MakeDefault`, plus optional extra labels/annotations). When `MakeDefault=true` both the GA and beta `is-default-class` annotations are set. Idempotent: `AlreadyExists` is logged and treated as success.

## VolumeSnapshotClass

`pkg/kubernetes/volumesnapshotclass.go`

- `CreateVolumeSnapshotClass(ctx, kubeconfig, cfg)` — Creates a `snapshot.storage.k8s.io/v1 VolumeSnapshotClass` from `VolumeSnapshotClassConfig` (`Name`, `Driver`, `DeletionPolicy` defaulting to `Delete`, `Parameters`, `MakeDefault`). Idempotent: `AlreadyExists` is logged and treated as success.
- `WaitForVolumeSnapshotClass(ctx, kubeconfig, name, timeout)` — Polls until the named VolumeSnapshotClass is Get-able.

## BlockDevice

`pkg/kubernetes/blockdevice.go`

- `GetConsumableBlockDevices(ctx, kubeconfig)` — Returns all consumable BlockDevices from the cluster.
- `GetConsumableBlockDevicesByNode(ctx, kubeconfig, nodeName)` — Returns consumable BlockDevices for a specific node.
- `LabelBlockDevice(ctx, kubeconfig, name, labelKey, labelValue)` — Sets a label on a single `BlockDevice` CR (via the dynamic client on `BlockDeviceGVR`). Idempotent (skips the update when the label already matches) and tolerant of optimistic-concurrency conflicts. Used to mark disks eligible for adoption by an `ElasticCluster.spec.storage.blockDeviceSelector`.

## LVMVolumeGroup

`pkg/kubernetes/lvmvolumegroup.go`

- `CreateLVMVolumeGroup(ctx, kubeconfig, name, nodeName, blockDeviceNames, actualVGName)` — Creates an LVMVolumeGroup resource for a specific node.
- `CreateLVMVolumeGroupWithThinPool(ctx, kubeconfig, name, nodeName, blockDeviceNames, actualVGName, thinPools)` — Creates an LVMVolumeGroup resource with thin pools for a specific node.
- `WaitForLVMVolumeGroupReady(ctx, kubeconfig, name, timeout)` — Waits for an LVMVolumeGroup to become Ready.
- `DeleteLVMVolumeGroup(ctx, kubeconfig, name)` — Deletes an LVMVolumeGroup resource by name.
- `WaitForLVMVolumeGroupDeletion(ctx, kubeconfig, name, timeout)` — Waits for an LVMVolumeGroup to be deleted.

## LocalStorageClass

`pkg/kubernetes/localstorageclass.go`

- `CreateLocalStorageClass(ctx, kubeconfig, cfg)` — Creates a LocalStorageClass CR from `LocalStorageClassConfig` (name, LVM volume groups, LVM type Thick/Thin, thin pool name, reclaim policy, volume binding mode). Idempotent if already exists.
- `WaitForLocalStorageClassCreated(ctx, kubeconfig, name, timeout)` — Waits for the LocalStorageClass CR status phase to reach `Created` (controller has created the corresponding StorageClass).

## VirtualDisk

`pkg/kubernetes/virtualdisk.go`

- `AttachVirtualDiskToVM(ctx, kubeconfig, config)` — Creates a blank VirtualDisk and attaches it to a VM using VirtualMachineBlockDeviceAttachment. Returns created resource names.
- `ReattachVirtualDiskToVM(ctx, kubeconfig, config)` — Attaches an existing VirtualDisk to a VM by creating a VirtualMachineBlockDeviceAttachment with explicit names.
- `WaitForVirtualDiskAttached(ctx, kubeconfig, namespace, attachmentName, pollInterval)` — Waits for a VirtualMachineBlockDeviceAttachment to reach the Attached phase.
- `ListVirtualMachineNames(ctx, kubeconfig, namespace)` — Lists VM names in a namespace.
- `GetVMIPFromBaseCluster(ctx, baseKubeconfig, namespace, vmName)` — Returns VM IP address from status (for SSH connections).
- `DetachAndDeleteVirtualDisk(ctx, kubeconfig, namespace, attachmentName, diskName)` — Deletes attachment then disk (cleanup helper; logs errors).

## VM Pod

`pkg/kubernetes/vmpod.go`

- `GetVMPodNodeAndContainerID(ctx, baseConfig, namespace, vmName)` — Finds the virt-launcher pod for a VM and returns node name and first container ID.

## Secrets

`pkg/kubernetes/secrets.go`

- `FindSecretByName(ctx, kubeconfig, namespace, name)` — Finds a secret by name using exact, case-insensitive, and fuzzy matching (handles Unicode issues). Returns the actual name found.
- `GetSecretDataValue(ctx, kubeconfig, namespace, name, key)` — Retrieves a specific data value from a secret. Uses `FindSecretByName` for Unicode-safe lookup.

## Modules

`pkg/kubernetes/modules.go`

- `EnableModulesWithSpecs(ctx, kubeconfig, sshClient, clusterDef, modules)` — Enables and configures modules with automatic dependency resolution via topological sort.
- `WaitForModulesReadyWithSpecs(ctx, kubeconfig, clusterDef, modules, timeout)` — Waits for specified modules to become ready after enabling.
- `EnableModulesAndWait(ctx, kubeconfig, sshClient, clusterDef, modules, timeout)` — Convenience function: enables modules and waits for them to become ready in one call.
- `EnableAndConfigureModules(ctx, kubeconfig, clusterDef, sshClient)` — Enables and configures modules from cluster definition, processing dependency levels sequentially.
- `WaitForModulesReady(ctx, kubeconfig, clusterDef, timeout)` — Waits for all modules from cluster definition to be ready, level by level.
- `WaitForModuleReady(ctx, kubeconfig, moduleName, timeout)` — Waits for a single module to reach Ready phase. Tolerates transient Error phases.

## Retry

`pkg/retry/retry.go`

- `Do[T](ctx, cfg, operationName, fn)` — Generic retry function with exponential backoff for transient errors. Returns result of `fn`.
- `DoVoid(ctx, cfg, operationName, fn)` — Like `Do` but for functions that return only an error.
- `IsRetryable(err)` — Checks if an error is transient (network, K8s API, SSH, webhook errors) and should be retried.
- `IsSSHConnectionError(err)` — Checks if an error specifically indicates SSH connection failure requiring reconnection.
- `WithRetryAfter(cfg, err)` — Returns a modified retry config that respects `RetryAfterSeconds` hints from Kubernetes API errors.

## Rook Config Override

`pkg/kubernetes/rookconfigoverride.go`

- `SetRookConfigOverride(ctx, kubeconfig, namespace, globals)` — Creates or updates the `rook-config-override` ConfigMap in the Rook operator namespace. The provided map is rendered under `[global]` and Rook picks it up into every Ceph daemon's `ceph.conf` (used for `ms_crc_data`, `bdev_enable_discard`, and similar knobs). Keys are sorted for stable output.
- `DeleteRookConfigOverride(ctx, kubeconfig, namespace)` — Removes the ConfigMap; safe if it does not exist.
- `RenderCephGlobalConfig(globals)` — Pure helper that renders a `[global]` section for `ceph.conf` from a `map[string]string`. Keys are sorted so the output is byte-stable across calls with logically-equivalent maps (used by `SetRookConfigOverride` to avoid spurious ConfigMap updates and by callers that need to compare the desired vs. live ConfigMap content before deciding to roll daemons).

## CephFilesystem (Rook)

`pkg/kubernetes/cephfilesystem.go`

- `CreateCephFilesystem(ctx, kubeconfig, cfg)` — Creates or updates a Rook `CephFilesystem` from `CephFilesystemConfig` (one replicated metadata pool + one replicated data pool, configurable `failureDomain`, `MetadataServerActiveCount`, optional `RequireSafeReplicaSize`). Idempotent. **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `WaitForCephFilesystemReady(ctx, kubeconfig, namespace, name, timeout)` — Polls until `status.phase == "Ready"`, with a fallback that also accepts `status.conditions[type=Ready,status=True]` for Rook revisions that populate conditions before phase. Each Get is bounded by `PollGetTimeout` (30s) and consecutive Get failures emit WARN, so a dropped SSH tunnel surfaces in seconds instead of after the readyTimeout. Fail-fast on `deletionTimestamp != nil`.
- `DeleteCephFilesystem(ctx, kubeconfig, namespace, name)` — Fire-and-forget delete; NotFound is treated as success. Pair with `WaitForCephFilesystemGone` to make sure the parent CephCluster's deletion isn't blocked by `ObjectHasDependents`.
- `WaitForCephFilesystemGone(ctx, kubeconfig, namespace, name, timeout)` — Polls until the CR is GC'd (default `CephFilesystemGoneTimeout` = 5m). Logs progress periodically.
- `CephFSDataPoolFullName(fsName, dataPoolName)` — Returns the full Ceph pool name (`<fsName>-<dataPoolName>`) that should be passed to `CephStorageClass.spec.cephFS.pool`.

## ElasticCluster / ElasticStorageClass (sds-elastic)

`pkg/kubernetes/elasticcluster.go`, `pkg/kubernetes/elasticstorageclass.go`

Low-level helpers over the cluster-scoped `storage.deckhouse.io/v1alpha1` `ElasticCluster` (`ec`) and `ElasticStorageClass` (`esc`) CRs. Both are addressed as `unstructured` via the dynamic client, so storage-e2e takes no build dependency on the sds-elastic module. Condition types and teardown reasons are mirrored as plain-string constants (`ElasticClusterCondition*`, `ElasticClusterReason*`, `ElasticStorageClassCondition*`, `ElasticStorageClassReason*`) — keep in sync with `sds-elastic/api/v1alpha1`.

- `CreateElasticCluster(ctx, kubeconfig, params)` — Creates or updates an `ElasticCluster` from `ElasticClusterParams` (name + `nodeSelector` / `blockDeviceSelector` matchLabels + optional `network.{public,cluster}`). Idempotent; **fail-fast** on a Terminating existing CR.
- `WaitForElasticClusterCondition(ctx, kubeconfig, name, condType, wantStatus, timeout)` — Blocks until the EC has the named status condition at the wanted status. Refuses to wait on a Terminating object.
- `WaitForElasticClusterReady(ctx, kubeconfig, name, timeout)` — Convenience: waits for `Ready=True`.
- `GetElasticClusterCondition(ctx, kubeconfig, name, condType)` — Single GET returning `(status, reason, message, found)`; wrap in a Gomega `Eventually`/`Consistently` to assert teardown-guard reasons on a Terminating CR.
- `GetElasticClusterCephTopology(ctx, kubeconfig, name)` — Reads `status.cephTopology` (effective mon/mgr counts + promotion reason). `found` is false until the controller records a topology.
- `DeleteElasticCluster(ctx, kubeconfig, name)` / `WaitForElasticClusterGone(ctx, kubeconfig, name, timeout)` — Fire-and-forget delete + GC wait (default `ElasticClusterGoneTimeout` = 15m).
- `CreateElasticStorageClass(ctx, kubeconfig, params)` — Creates or updates an `ElasticStorageClass` from `ElasticStorageClassParams` (`clusterRef`, `type` RBD/CephFS, `replication`). Idempotent; fail-fast on Terminating.
- `WaitForElasticStorageClassCondition` / `WaitForElasticStorageClassReady` / `GetElasticStorageClassCondition` — Same shape as the EC helpers.
- `AnnotateElasticStorageClassForceDeletion(ctx, kubeconfig, name)` — Sets `sds-elastic.deckhouse.io/force-deletion=true`, authorising the destructive purge of a non-empty RBD pool; never bypasses the bound-PV guard.
- `DeleteElasticStorageClass(ctx, kubeconfig, name)` / `WaitForElasticStorageClassGone(ctx, kubeconfig, name, timeout)` — Fire-and-forget delete + GC wait (default `ElasticStorageClassGoneTimeout` = 10m).

## Rook verifiers (sds-elastic renamed group)

`pkg/kubernetes/elasticrook.go`

The sds-elastic module ships a vendored Rook operator whose API group is renamed from upstream `ceph.rook.io` to `internal.sdselastic.deckhouse.io`. These helpers verify the Rook resources the EC controller created and that no upstream group leaked.

- `WaitForElasticRookCephClusterReady(ctx, kubeconfig, namespace, name, timeout)` — Blocks until the renamed-group `CephCluster` reports `state=Created` (or `phase=Ready`).
- `WaitForElasticRookCephBlockPoolReady` / `WaitForElasticRookCephFilesystemReady` — Block until the renamed-group pool/filesystem reports `status.phase=Ready`.
- `ListElasticRookCephClusterNames(ctx, kubeconfig, namespace)` — Names of all renamed-group `CephCluster`s in the namespace.
- `ServerHasAPIGroup(ctx, kubeconfig, group)` — Discovery check; used to assert the upstream `ceph.rook.io` group is absent on a cluster running sds-elastic.

## Default StorageClass (Testkit)

`pkg/testkit/storageclass.go`

- `CreateDefaultStorageClass(ctx, kubeconfig, cfg)` — High-level helper: discovers nodes, enables sds-node-configurator/sds-local-volume modules, labels nodes, optionally attaches VirtualDisks, creates LVMVolumeGroups (Thick or Thin with thin pool), creates LocalStorageClass, waits for StorageClass. Configured via `DefaultStorageClassConfig`.
- `EnsureDefaultStorageClass(ctx, kubeconfig, cfg)` — Idempotent wrapper around `CreateDefaultStorageClass`. Checks if StorageClass already exists, skips creation if so, then sets it as the cluster default via "global" ModuleConfig.

## Elastic (Testkit)

`pkg/testkit/elastic.go`

High-level helpers that drive the sds-elastic stack end-to-end. They assume the modules (`sds-node-configurator`, `csi-ceph`, `sds-elastic`) are already enabled on the cluster (e.g. via the suite's `cluster_config.yml`); module enablement is intentionally out of scope here. Type/replication enums are re-exported (`ElasticStorageClassType*`, `ElasticReplication*`).

- `EnsureElasticOSDBlockDevices(ctx, kubeconfig, cfg)` — Prepares raw disks for OSD adoption: resolves storage nodes (explicit list or all workers), labels them, waits for `>= MinBlockDevices` consumable `BlockDevice`s to surface on them, then labels those BDs. Returns the labelled BD names. Node/BD label key/value default to the `sds-elastic-e2e.storage.deckhouse.io/*` constants and must match the EC selectors.
- `EnsureElasticCluster(ctx, kubeconfig, cfg)` — Creates (or reuses) an `ElasticCluster` with the given selectors and waits until `Ready` (default 25m). Returns the EC name.
- `EnsureElasticStorageClass(ctx, kubeconfig, cfg)` — Creates (or reuses) an `ElasticStorageClass`, waits until `Ready` (default 10m), and confirms the 1:1-named core StorageClass exists. Returns the StorageClass name.
- `TeardownElasticStorageClass(ctx, kubeconfig, name, force, timeout)` — Optionally sets the force-deletion annotation, deletes the ESC, and waits until it is gone. Force authorises an RBD pool purge but never bypasses the bound-PV guard.
- `TeardownElasticCluster(ctx, kubeconfig, name, timeout)` — Deletes the EC and waits until the controller finalizer (Rook CephCluster + csi-ceph teardown) completes. Tear down referencing ESCs first, or the EC sticks on the non-bypassable `StorageClassesExist` guard.

## Stress Tests (Testkit)

`pkg/testkit/stress-tests.go`

- `DefaultConfig()` — Returns stress test config with defaults from environment variables.
- `NewStressTestRunner(config, restConfig)` — Creates a new stress test runner with Kubernetes clientset and dynamic client.
- `(*Config) Validate()` — Validates the stress test configuration (namespace, storage class, PVC size, mode-specific params).
- `(*StressTestRunner) Run(ctx)` — Executes the stress test based on configured mode: flog, check_fs_only, check_cloning, check_restoring_from_snapshot, snapshot_only, or snapshot_resize_cloning.
- `CleanupStressNamespaces(ctx, kubeconfig)` — Deletes all namespaces with the `load-test=true` label.

## Ceph CRC (Testkit)

`pkg/testkit/ceph_crc.go`

- `EnableServerCRC(ctx, kubeconfig, namespace)` — Sets `ms_crc_data=true` on the server side: rewrites `rook-config-override` and rolling-restarts every Rook-managed Ceph daemon Deployment (mon/mgr/osd/mds/rgw) plus the rook-operator. Use when a test wants Ceph pinned in the explicit CRC-on state. Thin wrapper over `SetMsCrcDataOnServer(..., ptr(true))`.
- `DisableServerCRC(ctx, kubeconfig, namespace)` — Same as `EnableServerCRC` but flips Ceph into `ms_crc_data=false`. Paired with a csi-ceph client that defaults to `msCrcData=true` this reproduces the msCrcData matrix mismatch case. Thin wrapper over `SetMsCrcDataOnServer(..., ptr(false))`.
- `ResetServerCRCToDefault(ctx, kubeconfig, namespace)` — Removes `ms_crc_data` from `rook-config-override` so Ceph falls back to its compile-time default (`true`). Convenient for `AfterAll` / `AfterEach` restoration. Thin wrapper over `SetMsCrcDataOnServer(..., nil)`.
- `SetMsCrcDataOnServer(ctx, kubeconfig, namespace, enabled *bool)` — Lower-level primitive behind the three readability wrappers. Rewrites `rook-config-override` so that only `ms_crc_data=<enabled>` ends up under `[global]` (`nil` removes the key entirely). Idempotent: when the ConfigMap already encodes the desired state, nothing is restarted. Otherwise it (1) rolling-restarts Rook-managed Ceph daemons via `RestartCephDaemons`, (2) restarts the rook-operator via `RestartRookOperator`, and (3) waits for every `CephFilesystem` in the namespace to come back to Ready. Prefer the named wrappers at call sites; this primitive exists so a boolean test parameter (e.g. a CRC matrix) doesn't have to branch.
- `RestartCephDaemons(ctx, kubeconfig, namespace, timeout)` — Rollout-restarts every Rook-managed Ceph daemon Deployment that consumes `/etc/ceph/ceph.conf` — the selector covers `rook-ceph-mon`, `rook-ceph-mgr`, `rook-ceph-osd`, `rook-ceph-mds`, `rook-ceph-rgw` — and waits for each to reach its desired Ready replica count. All five roles are bounced because a global ConfigMap knob like `ms_crc_data` lives in `ceph.conf` and any daemon left running with the old value (typically MDS) silently breaks the messenger handshake and degrades CephFS / blocks csi-cephfs PVCs in Pending. Operator restart is intentionally out of scope here — see `RestartRookOperator`.
- `RestartRookOperator(ctx, kubeconfig, namespace, timeout)` — Rollout-restarts the rook-operator Deployment in the given namespace and waits for the new pod to become Ready. Required after every wire-protocol bounce: the operator runs as a Ceph admin client (admin keyring + baked-in `ceph.conf`), and without a pod restart it keeps retrying with the stale `ceph.conf`, which surfaces in the cephcluster CR as `HEALTH_ERR` / `state: Error` until the next reconcile. Deckhouse-specific naming: the Deployment name is derived from the namespace by stripping the leading `d8-` prefix (`d8-sds-elastic` → `sds-elastic`). Vanilla Rook (`rook-ceph-operator` in `rook-ceph`) is not supported.
