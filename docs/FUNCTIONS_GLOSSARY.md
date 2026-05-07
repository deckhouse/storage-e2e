# Functions Glossary

All exported functions available in the `pkg/` directory, grouped by resource.

## Table of Contents

- [Cluster](#cluster)
- [Cluster Lock](#cluster-lock)
- [VM (Virtual Machine)](#vm-virtual-machine)
- [Setup / Bootstrap](#setup--bootstrap)
- [Kubernetes Client](#kubernetes-client)
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
- [Ceph Credentials](#ceph-credentials)
- [CephCluster (Rook)](#cephcluster-rook)
- [CephBlockPool (Rook)](#cephblockpool-rook)
- [CephFilesystem (Rook)](#cephfilesystem-rook)
- [CephClusterConnection / CephClusterAuthentication (csi-ceph)](#cephclusterconnection--cephclusterauthentication-csi-ceph)
- [CephStorageClass (csi-ceph)](#cephstorageclass-csi-ceph)
- [Default StorageClass (Testkit)](#default-storageclass-testkit)
- [Ceph StorageClass (Testkit)](#ceph-storageclass-testkit)
- [Ceph Cluster (Testkit) — no csi-ceph wiring](#ceph-cluster-testkit--no-csi-ceph-wiring)
- [Stress Tests (Testkit)](#stress-tests-testkit)
- [Ceph CRC (Testkit)](#ceph-crc-testkit)

---

## Cluster

`pkg/cluster/cluster.go`

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

## Cluster Lock

`pkg/cluster/lock.go`

- `AcquireClusterLock(ctx, kubeconfig, testName)` — Creates a ConfigMap lock to indicate the cluster is busy. Returns error if already locked.
- `ReleaseClusterLock(ctx, kubeconfig)` — Removes the cluster lock ConfigMap. Safe to call if lock doesn't exist.
- `IsClusterLocked(ctx, kubeconfig)` — Checks if the cluster is currently locked by looking for the lock ConfigMap.
- `GetClusterLockInfo(ctx, kubeconfig)` — Retrieves information about the current cluster lock holder.
- `ForceReleaseClusterLock(ctx, kubeconfig)` — Forcefully removes the cluster lock. Use only when sure no other test is running.

## VM (Virtual Machine)

`pkg/cluster/vms.go`

- `CreateVirtualMachines(ctx, virtClient, clusterDef)` — Creates all VMs from cluster definition in parallel. Handles name conflicts and returns VM names and resource tracking info.
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
- `ReadFileFromDistrolessPod(ctx, kubeconfig, namespace, pod, targetContainer, path, opts)` — Reads a file from a distroless / scratch container that ships no `cat`/`sh`/`tar`. Injects a short-lived ephemeral container (image from `opts.DebugImage`, defaults to `DefaultDebugImage = "busybox:1.36"`) with `targetContainerName=targetContainer`, polls until it goes Running (`opts.StartupTimeout`, defaults to 60s), then `cat /proc/1/root<path>` — `/proc/1/root` is the kernel-exposed FS root of PID 1 in the target container, which the ephemeral container can see thanks to the shared PID namespace. Adding the ephemeral container goes through the dedicated `/pods/<name>/ephemeralcontainers` subresource, so existing containers and the pod sandbox are NOT restarted, `metadata.generation` is not bumped, and ReplicaSet/DaemonSet observation is unaffected — downstream rollout / `checksum/...` annotation assertions still see a clean signal. Caveat: ephemeral containers cannot be removed once added, but each call generates a unique name and the `sleep 60` command exits on its own; entries pile up in `pod.status.ephemeralContainerStatuses` until the next pod recycle. Internally a one-shot wrapper around `OpenDistrolessReader` + `(*DistrolessReader).ReadFile`.
- `OpenDistrolessReader(ctx, kubeconfig, namespace, pod, targetContainer, opts) (*DistrolessReader, error)` — Long-lived variant of `ReadFileFromDistrolessPod`: injects ONE ephemeral container (sleeps for `opts.SessionTTL`, defaults to `DefaultDistrolessSessionTTL` = 30 min) and returns a session that can serve arbitrarily many cheap reads. Use this for polling loops (e.g. `Eventually(...)` waiting for a file's content to flip) so the ephemeral-container cold start is paid once instead of per iteration.
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

`pkg/kubernetes/storageclass_manage.go`

- `CreateStorageClass(ctx, kubeconfig, cfg)` — Creates a `storage.k8s.io/v1 StorageClass` directly from `StorageClassCreateConfig` (`Name`, `Provisioner`, `Parameters`, `VolumeBindingMode`, `ReclaimPolicy`, `AllowExpansion`, `MakeDefault`, plus optional extra labels/annotations). When `MakeDefault=true` both the GA and beta `is-default-class` annotations are set. Idempotent: `AlreadyExists` is logged and treated as success.

## VolumeSnapshotClass

`pkg/kubernetes/volumesnapshotclass.go`

- `CreateVolumeSnapshotClass(ctx, kubeconfig, cfg)` — Creates a `snapshot.storage.k8s.io/v1 VolumeSnapshotClass` from `VolumeSnapshotClassConfig` (`Name`, `Driver`, `DeletionPolicy` defaulting to `Delete`, `Parameters`, `MakeDefault`). Idempotent: `AlreadyExists` is logged and treated as success.
- `WaitForVolumeSnapshotClass(ctx, kubeconfig, name, timeout)` — Polls until the named VolumeSnapshotClass is Get-able.

## BlockDevice

`pkg/kubernetes/blockdevice.go`

- `GetConsumableBlockDevices(ctx, kubeconfig)` — Returns all consumable BlockDevices from the cluster.
- `GetConsumableBlockDevicesByNode(ctx, kubeconfig, nodeName)` — Returns consumable BlockDevices for a specific node.

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

## Ceph Credentials

`pkg/kubernetes/cephcredentials.go`

- `WaitForCephCredentials(ctx, kubeconfig, namespace, timeout)` — Polls Rook's `rook-ceph-mon` Secret and `rook-ceph-mon-endpoints` ConfigMap until all pieces required to connect a CSI client to the cluster (`fsid`, admin user, admin key, monitor endpoints) are present. Returns a `*CephCredentials`.

## CephCluster (Rook)

`pkg/kubernetes/cephcluster.go`

- `CreateCephCluster(ctx, kubeconfig, cfg)` — Creates or updates a Rook `CephCluster` CR using `CephClusterConfig` (image, mon/mgr counts, network provider, OSD storage class / count / size, data-dir host path, etc.). Idempotent. **Fail-fast:** if an existing CR has `metadata.deletionTimestamp != nil`, returns an error instead of trying to update a Terminating object (which would silently no-op and trap the next `WaitForCephClusterReady` for 15-20 minutes).
- `WaitForCephClusterReady(ctx, kubeconfig, namespace, name, timeout)` — Blocks until `status.state == "Created"` (or `status.phase == "Ready"`). HEALTH_WARN is tolerated so single-OSD test clusters still succeed. Each Get is bounded by `PollGetTimeout` (30s) and consecutive Get failures emit WARN, so a dropped SSH tunnel surfaces in seconds instead of after the readyTimeout. **Fail-fast** when the CR comes back with `deletionTimestamp != nil` — there's no point waiting for Ready on a Terminating object.
- `DeleteCephCluster(ctx, kubeconfig, namespace, name)` — Fire-and-forget delete; NotFound is treated as success. Does NOT garbage-collect OSD data on host disks. Pair with `WaitForCephClusterGone` if the next step depends on the CR being fully GC'd (e.g. before re-creating the cluster, or to detect a stuck `cephcluster.ceph.rook.io` finalizer early).
- `WaitForCephClusterGone(ctx, kubeconfig, namespace, name, timeout)` — Polls until the CR returns NotFound (default `CephClusterGoneTimeout` = 10m when timeout is 0). Logs deletionTimestamp / finalizers progress periodically, so a stuck finalizer (typical after a teardown that left dependents alive — see `DeletionIsBlocked`) is visible immediately instead of after a silent timeout. Fail-fast on timeout: does NOT auto-strip finalizers — investigate the cluster manually before re-running.

## CephBlockPool (Rook)

`pkg/kubernetes/cephblockpool.go`

- `CreateCephBlockPool(ctx, kubeconfig, cfg)` — Creates or updates a Rook `CephBlockPool` from `CephBlockPoolConfig` (replicated with optional `requireSafeReplicaSize` override, or erasure-coded with `dataChunks`/`codingChunks`; `failureDomain`). **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `WaitForCephBlockPoolReady(ctx, kubeconfig, namespace, name, timeout)` — Polls until `status.phase == "Ready"`. Each Get is bounded by `PollGetTimeout` (30s) and consecutive Get failures emit WARN, so a dropped SSH tunnel surfaces in seconds instead of after the readyTimeout. Fail-fast on `deletionTimestamp != nil`.
- `DeleteCephBlockPool(ctx, kubeconfig, namespace, name)` — Fire-and-forget delete; NotFound is treated as success. Pair with `WaitForCephBlockPoolGone` to make sure the parent CephCluster's deletion isn't blocked by `ObjectHasDependents`.
- `WaitForCephBlockPoolGone(ctx, kubeconfig, namespace, name, timeout)` — Polls until the CR is GC'd (default `CephBlockPoolGoneTimeout` = 5m). Logs progress periodically.

## CephFilesystem (Rook)

`pkg/kubernetes/cephfilesystem.go`

- `CreateCephFilesystem(ctx, kubeconfig, cfg)` — Creates or updates a Rook `CephFilesystem` from `CephFilesystemConfig` (one replicated metadata pool + one replicated data pool, configurable `failureDomain`, `MetadataServerActiveCount`, optional `RequireSafeReplicaSize`). Idempotent. **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `WaitForCephFilesystemReady(ctx, kubeconfig, namespace, name, timeout)` — Polls until `status.phase == "Ready"`, with a fallback that also accepts `status.conditions[type=Ready,status=True]` for Rook revisions that populate conditions before phase. Each Get is bounded by `PollGetTimeout` (30s) and consecutive Get failures emit WARN, so a dropped SSH tunnel surfaces in seconds instead of after the readyTimeout. Fail-fast on `deletionTimestamp != nil`.
- `DeleteCephFilesystem(ctx, kubeconfig, namespace, name)` — Fire-and-forget delete; NotFound is treated as success. Pair with `WaitForCephFilesystemGone` to make sure the parent CephCluster's deletion isn't blocked by `ObjectHasDependents`.
- `WaitForCephFilesystemGone(ctx, kubeconfig, namespace, name, timeout)` — Polls until the CR is GC'd (default `CephFilesystemGoneTimeout` = 5m). Logs progress periodically.
- `CephFSDataPoolFullName(fsName, dataPoolName)` — Returns the full Ceph pool name (`<fsName>-<dataPoolName>`) that should be passed to `CephStorageClass.spec.cephFS.pool`.

## CephClusterConnection / CephClusterAuthentication (csi-ceph)

`pkg/kubernetes/cephclusterconnection.go`

- `CreateCephClusterAuthentication(ctx, kubeconfig, cfg)` — Creates or updates a `CephClusterAuthentication` CR (`userID` + `userKey`) used by csi-ceph to log in to Ceph. **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `DeleteCephClusterAuthentication(ctx, kubeconfig, name)` — Fire-and-forget delete; NotFound is treated as success.
- `WaitForCephClusterAuthenticationGone(ctx, kubeconfig, name, timeout)` — Polls until the CR is GC'd (default `CephClusterAuthenticationGoneTimeout` = 1m).
- `CreateCephClusterConnection(ctx, kubeconfig, cfg)` — Creates or updates a `CephClusterConnection` CR (`clusterID == fsid`, `monitors`, `userID`, `userKey`). `clusterID` is immutable: existing-resource updates leave it unchanged and only sync monitors/user. **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `DeleteCephClusterConnection(ctx, kubeconfig, name)` — Fire-and-forget delete; NotFound is treated as success.
- `WaitForCephClusterConnectionGone(ctx, kubeconfig, name, timeout)` — Polls until the CR is GC'd (default `CephClusterConnectionGoneTimeout` = 1m).
- `WaitForCephClusterConnectionCreated(ctx, kubeconfig, name, timeout)` — Polls until csi-ceph reports `status.phase == "Created"` (credentials + monitors validated against the live Ceph cluster).

## CephStorageClass (csi-ceph)

`pkg/kubernetes/cephstorageclass.go`

- `CreateCephStorageClass(ctx, kubeconfig, cfg)` — Creates or updates a csi-ceph `CephStorageClass` CR (RBD by default; CephFS when `Type == "CephFS"` and `CephFSName` / `CephFSPool` are set). The csi-ceph controller provisions a corresponding core `storage.k8s.io/v1 StorageClass` as a side effect. **Fail-fast** when the existing CR has `deletionTimestamp != nil`.
- `DeleteCephStorageClass(ctx, kubeconfig, name)` — Fire-and-forget delete; the controller removes the backing StorageClass.
- `WaitForCephStorageClassGone(ctx, kubeconfig, name, timeout)` — Polls until the CR is GC'd (default `CephStorageClassGoneTimeout` = 1m).
- `WaitForCephStorageClassCreated(ctx, kubeconfig, name, timeout)` — Polls until `status.phase == "Created"`.

## Default StorageClass (Testkit)

`pkg/testkit/storageclass.go`

- `CreateDefaultStorageClass(ctx, kubeconfig, cfg)` — High-level helper: discovers nodes, enables sds-node-configurator/sds-local-volume modules, labels nodes, optionally attaches VirtualDisks, creates LVMVolumeGroups (Thick or Thin with thin pool), creates LocalStorageClass, waits for StorageClass. Configured via `DefaultStorageClassConfig`.
- `EnsureDefaultStorageClass(ctx, kubeconfig, cfg)` — Idempotent wrapper around `CreateDefaultStorageClass`. Checks if StorageClass already exists, skips creation if so, then sets it as the cluster default via "global" ModuleConfig.

## Ceph StorageClass (Testkit)

`pkg/testkit/ceph.go`

- `EnsureCephStorageClass(ctx, kubeconfig, cfg)` — High-level end-to-end helper that turns an empty test cluster into one with a working csi-ceph `StorageClass`. Steps: (1) enable `sds-node-configurator`, `sds-elastic`, `csi-ceph` modules and wait Ready; (2) optionally call `EnsureDefaultStorageClass` to auto-provision a sds-local-volume SC for OSDs when `OSDStorageClass` is empty; (3) seed `rook-config-override` with `GlobalCephConfigOverrides` (e.g. `ms_crc_data=false`); (4) create Rook `CephCluster` and wait Created; (5) create the backing pool primitive — `CephBlockPool` (when `Type == "RBD"`, default) or `CephFilesystem` (when `Type == "CephFS"`) — and wait Ready; (6) read fsid/monitors/admin-key from Rook-managed secrets; (7) wire csi-ceph by creating `CephClusterAuthentication` + `CephClusterConnection`; (8) create the matching `CephStorageClass` (RBD pool or `<fsName>-<dataPoolName>` for CephFS) and wait for the backing core StorageClass. Idempotent; returns the resulting StorageClass name.
- `EnsureDefaultCephStorageClass(ctx, kubeconfig, cfg)` — `EnsureCephStorageClass` + `SetGlobalDefaultStorageClass` so new PVCs without an explicit `storageClassName` use the provisioned Ceph (RBD or CephFS) class.
- `TeardownCephStorageClass(ctx, kubeconfig, cfg)` — Reverse of `EnsureCephStorageClass`. After every Delete it now waits for the CR to be fully GC'd via the matching `WaitForXxxGone` helper. Order is: `CephStorageClass` → `CephClusterConnection` → `CephClusterAuthentication` → (`CephBlockPool` or `CephFilesystem` per `cfg.Type`) → `CephCluster` (unless `SkipClusterTeardown`) → `rook-config-override` ConfigMap. Without those waits the parent `CephCluster` would be deleted before its dependents are gone, Rook would record `DeletionIsBlocked / ObjectHasDependents`, and the next test run would either find a stuck Terminating CR or hang in `WaitForCephClusterReady`. Fail-fast on a Wait*Gone timeout: errors are aggregated and returned, no auto-strip of finalizers — investigate the cluster manually before re-running. NotFound is still treated as success; subsequent deletions are still attempted on partial failures.

## Ceph Cluster (Testkit) — no csi-ceph wiring

`pkg/testkit/ceph_cluster.go`

- `EnsureCephCluster(ctx, kubeconfig, cfg)` — "Stop-before-csi-ceph" variant of `EnsureCephStorageClass`: brings up a Rook-managed Ceph cluster + CephBlockPool via sds-elastic alone. Steps: (1) enable `sds-node-configurator` + `sds-elastic` (does **not** enable `csi-ceph`); (2) resolve/provision OSD backing StorageClass (reuses `EnsureDefaultStorageClass`); (3) seed `rook-config-override` with `GlobalCephConfigOverrides`; (4) create Rook `CephCluster` and wait Created; (5) create `CephBlockPool` and wait Ready. Does not create `CephClusterConnection`/`CephClusterAuthentication`/`CephStorageClass`. Useful when tests need a live Ceph backend to talk to directly (e.g. from within csi-ceph's own e2e) without the testkit preselecting a csi-ceph-backed StorageClass. Idempotent; returns the pool name.

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
