/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package unified_snapshots

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

// ── Contract literals (single source of truth: state-snapshotter delete-protection design) ──
const (
	deleteProtectedLabel = "state-snapshotter.deckhouse.io/delete-protected"
	breakGlassAnnotation = "deckhouse.io/allow-delete"

	// deleteGuardPolicy is the ValidatingAdmissionPolicy (and binding) name that enforces delete-protection.
	deleteGuardPolicy = "d8-state-snapshotter-delete-guard"

	// envDeleteGuard gates the strict deny-expecting specs. The module ships the VAP in enforcement=Audit;
	// switching to Deny is a deliberate operator rollout (after the backfill gate). Under Audit a direct
	// DELETE of a protected object is admitted (with a warning), so the Forbidden assertions only hold when
	// the operator has switched the policy to Deny and set this to "true".
	envDeleteGuard = "E2E_DELETE_GUARD"
)

// ── Overridable API coordinates (default: sds-unified-snapshots-poc demo controllers) ──
var (
	demoGroup    = envOr("SNAP_DEMO_GROUP", "sds-unified-snapshots-poc.deckhouse.io")
	demoVersion  = envOr("SNAP_DEMO_VERSION", "v1alpha1")
	coreGroup    = envOr("SNAP_CORE_GROUP", "state-snapshotter.deckhouse.io")
	storageClass = envOr("SNAP_STORAGE_CLASS", "local-thin")
	bindImage    = envOr("SNAP_BIND_IMAGE", "registry.k8s.io/pause:3.9")

	vdGVR     = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualdisks"}
	vmGVR     = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualmachines"}
	vdSnapGVR = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualdisksnapshots"}
	vmSnapGVR = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualmachinesnapshots"}

	// SnapshotContent is cluster-scoped (<coreGroup>/v1alpha1).
	snapContentGVR = schema.GroupVersionResource{Group: coreGroup, Version: "v1alpha1", Resource: "snapshotcontents"}

	pvcGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}
	podGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	nsGVR  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
)

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// connectCluster returns a rest.Config and a cleanup func. When SNAP_KUBECONFIG is set it builds the
// client directly from that kubeconfig file (e.g. a persistent local tunnel at 127.0.0.1:6445) and does
// NO SSH — convenient for ad-hoc runs against an already-reachable cluster. Otherwise it opens an SSH
// tunnel via the storage-e2e framework (connectExisting).
func connectCluster(ctx context.Context) (*rest.Config, func()) {
	if kc := strings.TrimSpace(os.Getenv("SNAP_KUBECONFIG")); kc != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", expandHome(kc))
		Expect(err).NotTo(HaveOccurred(), "failed to build rest.Config from SNAP_KUBECONFIG=%s", kc)
		return cfg, func() {}
	}
	res := connectExisting(ctx)
	cleanup := func() {
		if res.TunnelInfo != nil && res.TunnelInfo.StopFunc != nil {
			res.TunnelInfo.StopFunc()
		}
		if res.SSHClient != nil {
			res.SSHClient.Close()
		}
	}
	return res.Kubeconfig, cleanup
}

// connectExisting opens an SSH tunnel to the existing cluster and returns its resources.
func connectExisting(ctx context.Context) *cluster.TestClusterResources {
	key := expandHome(config.SSHPrivateKey)
	opts := cluster.ConnectClusterOptions{
		SSHUser:             config.SSHUser,
		SSHHost:             config.SSHHost,
		SSHKeyPath:          key,
		KubeconfigOutputDir: config.E2ETempDir,
	}
	if config.SSHJumpHost != "" {
		jumpUser := config.SSHJumpUser
		if jumpUser == "" {
			jumpUser = config.SSHUser
		}
		jumpKey := key
		if config.SSHJumpKeyPath != "" {
			jumpKey = expandHome(config.SSHJumpKeyPath)
		}
		opts = cluster.ConnectClusterOptions{
			SSHUser:             jumpUser,
			SSHHost:             config.SSHJumpHost,
			SSHKeyPath:          jumpKey,
			UseJumpHost:         true,
			JumpHostUser:        jumpUser,
			JumpHostHost:        config.SSHJumpHost,
			JumpHostKeyPath:     jumpKey,
			TargetUser:          config.SSHUser,
			TargetHost:          config.SSHHost,
			TargetKeyPath:       key,
			KubeconfigOutputDir: config.E2ETempDir,
		}
	}
	res, err := cluster.ConnectToCluster(ctx, opts)
	Expect(err).NotTo(HaveOccurred(), "Failed to connect to existing cluster")
	Expect(res).NotTo(BeNil())
	Expect(res.Kubeconfig).NotTo(BeNil())
	return res
}

// ── object builders ──

func nsObject(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Namespace")
	u.SetName(name)
	return u
}

// demoDisk is a blank DemoVirtualDisk that provisions its own backing PVC from size+storageClassName.
func demoDisk(ns, name, pvc string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": demoGroup + "/" + demoVersion,
		"kind":       "DemoVirtualDisk",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"persistentVolumeClaimName": pvc,
			"size":                      "1Gi",
			"storageClassName":          storageClass,
		},
	}}
}

func demoVM(ns, name, diskName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": demoGroup + "/" + demoVersion,
		"kind":       "DemoVirtualMachine",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec":       map[string]interface{}{"virtualDiskName": diskName},
	}}
}

// bindPod mounts a PVC so a WaitForFirstConsumer StorageClass binds it and keeps it attached.
func bindPod(ns, name, claim string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"restartPolicy": "Always",
			"containers": []interface{}{map[string]interface{}{
				"name":         "hold",
				"image":        bindImage,
				"volumeMounts": []interface{}{map[string]interface{}{"name": "v", "mountPath": "/data"}},
			}},
			"volumes": []interface{}{map[string]interface{}{
				"name":                  "v",
				"persistentVolumeClaim": map[string]interface{}{"claimName": claim},
			}},
		},
	}}
}

func demoSnapshot(kind, name, ns, sourceKind, sourceName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": demoGroup + "/" + demoVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"sourceRef": map[string]interface{}{
				"apiVersion": demoGroup + "/" + demoVersion,
				"kind":       sourceKind,
				"name":       sourceName,
			},
		},
	}}
}

// ── status / marker helpers ──

func condStatus(u *unstructured.Unstructured, condType string) string {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == condType {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

func isProtected(u *unstructured.Unstructured) bool {
	return u.GetLabels()[deleteProtectedLabel] == "true"
}

// isGuardDenial reports whether err is an admission denial produced by our delete-guard VAP.
// A VAP validation without an explicit reason surfaces as HTTP 422 (Invalid) rather than 403
// (Forbidden), even though the apiserver still phrases the message as
//
//	"... is forbidden: ValidatingAdmissionPolicy '<name>' ... denied request: <message>".
//
// We therefore accept either Forbidden or Invalid and pin the denial to our policy by name, so the
// assertion still proves that *our* guard blocked the request (not some unrelated rejection).
func isGuardDenial(err error) bool {
	if err == nil {
		return false
	}
	if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
		return false
	}
	return strings.Contains(err.Error(), deleteGuardPolicy)
}

func waitReady(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		obj, err := get(ctx, dyn, gvr, ns, name)
		if err == nil && condStatus(obj, "Ready") == "True" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s/%s Ready", gvr.Resource, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func get(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	if ns == "" {
		return dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	return dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}

func create(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured) error {
	var err error
	if ns == "" {
		_, err = dyn.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
	} else {
		_, err = dyn.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	}
	return err
}

// annotateAllowDelete stamps the break-glass annotation (leaves the marker intact).
func annotateAllowDelete(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns, name string) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:"true"}}}`, breakGlassAnnotation))
	var err error
	if ns == "" {
		_, err = dyn.Resource(gvr).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	} else {
		_, err = dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

// deleteBreakGlass stamps break-glass then deletes; used for cleanup of Retain-policy content.
func deleteBreakGlass(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns, name string) {
	_ = annotateAllowDelete(ctx, dyn, gvr, ns, name)
	if ns == "" {
		_ = dyn.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
	} else {
		_ = dyn.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
	}
}

func listNames(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns string) ([]string, error) {
	var lst *unstructured.UnstructuredList
	var err error
	if ns == "" {
		lst, err = dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
	} else {
		lst, err = dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(lst.Items))
	for i := range lst.Items {
		names = append(names, lst.Items[i].GetName())
	}
	return names, nil
}

// ── suite ──

var _ = Describe("Unified snapshot SDK write-path and delete-protection", Ordered, func() {
	const (
		diskStandalone = "disk-standalone"
		diskVM         = "disk-vm"
		vmName         = "vm-1"
		leafSnap       = "vds-leaf"
		treeSnap       = "vms-tree"
	)
	var (
		cleanup   = func() {}
		dyn       dynamic.Interface
		ns        = envOr("SNAP_NAMESPACE", fmt.Sprintf("uni-snap-%d", time.Now().Unix()))
		baseline  = map[string]struct{}{} // SnapshotContents present before the test (for delta cleanup)
		readyTO   = 5 * time.Minute
		guardDeny = envBool(os.Getenv(envDeleteGuard))
	)

	BeforeAll(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()

		By("Connecting to the cluster")
		var cfg *rest.Config
		cfg, cleanup = connectCluster(ctx)
		var err error
		dyn, err = dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		By("Recording the pre-existing SnapshotContent set (delta cleanup baseline)")
		if names, lerr := listNames(ctx, dyn, snapContentGVR, ""); lerr == nil {
			for _, n := range names {
				baseline[n] = struct{}{}
			}
		}

		By("Materializing the demo tree: standalone disk + VM-owned disk + VM, with consumer pods")
		Expect(create(ctx, dyn, nsGVR, "", nsObject(ns))).To(Succeed())
		Expect(create(ctx, dyn, vdGVR, ns, demoDisk(ns, diskStandalone, "pvc-sa"))).To(Succeed())
		Expect(create(ctx, dyn, vdGVR, ns, demoDisk(ns, diskVM, "pvc-vm"))).To(Succeed())
		Expect(create(ctx, dyn, vmGVR, ns, demoVM(ns, vmName, diskVM))).To(Succeed())
		// WaitForFirstConsumer: bind both backing PVCs (the VM controller only creates its mounting pod
		// once its disk is Ready, which cannot happen until the PVC binds — so we break the deadlock).
		Expect(create(ctx, dyn, podGVR, ns, bindPod(ns, "consumer-sa", "pvc-sa"))).To(Succeed())
		Expect(create(ctx, dyn, podGVR, ns, bindPod(ns, "consumer-vm", "pvc-vm"))).To(Succeed())

		By("Waiting for both disks and the VM to become Ready")
		Expect(waitReady(ctx, dyn, vdGVR, ns, diskStandalone, readyTO)).To(Succeed())
		Expect(waitReady(ctx, dyn, vdGVR, ns, diskVM, readyTO)).To(Succeed())
		Expect(waitReady(ctx, dyn, vmGVR, ns, vmName, readyTO)).To(Succeed())
	})

	AfterAll(func() {
		defer cleanup()
		if dyn == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		By("Break-glass cleanup of Retain-policy SnapshotContents created by this run")
		if names, err := listNames(ctx, dyn, snapContentGVR, ""); err == nil {
			for _, n := range names {
				if _, existed := baseline[n]; !existed {
					deleteBreakGlass(ctx, dyn, snapContentGVR, "", n)
				}
			}
		}

		By("Deleting the namespace (cascade removes sources, pods, remaining domain snapshots)")
		_ = dyn.Resource(nsGVR).Delete(ctx, ns, metav1.DeleteOptions{})
	})

	// ── Group A: updated SDK capture write-path ──

	It("leaf (VirtualDisk) and tree (VirtualMachine) snapshots become Ready (SDK phases + Ready mirror)", func() {
		ctx, cancel := context.WithTimeout(context.Background(), readyTO+time.Minute)
		defer cancel()

		Expect(create(ctx, dyn, vdSnapGVR, ns, demoSnapshot("DemoVirtualDiskSnapshot", leafSnap, ns, "DemoVirtualDisk", diskStandalone))).To(Succeed())
		Expect(create(ctx, dyn, vmSnapGVR, ns, demoSnapshot("DemoVirtualMachineSnapshot", treeSnap, ns, "DemoVirtualMachine", vmName))).To(Succeed())

		Expect(waitReady(ctx, dyn, vdSnapGVR, ns, leafSnap, readyTO)).To(Succeed())
		Expect(waitReady(ctx, dyn, vmSnapGVR, ns, treeSnap, readyTO)).To(Succeed())
	})

	It("stamps the delete-protected marker on system-created children and every SnapshotContent, but never on user-created roots", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		By("User-created roots (leaf + VM) must NOT carry the marker")
		for _, tc := range []struct {
			gvr  schema.GroupVersionResource
			name string
		}{{vdSnapGVR, leafSnap}, {vmSnapGVR, treeSnap}} {
			obj, err := get(ctx, dyn, tc.gvr, ns, tc.name)
			Expect(err).NotTo(HaveOccurred())
			Expect(isProtected(obj)).To(BeFalse(), "root %s/%s must be unmarked", tc.gvr.Resource, tc.name)
		}

		By("At least one system-created child DemoVirtualDiskSnapshot must carry the marker")
		lst, err := dyn.Resource(vdSnapGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		markedChildren := 0
		for i := range lst.Items {
			it := &lst.Items[i]
			if it.GetName() == leafSnap { // the leaf is a user-created root, skip
				continue
			}
			if isProtected(it) {
				markedChildren++
			}
		}
		Expect(markedChildren).To(BeNumerically(">=", 1), "expected at least one protected child disk snapshot from the VM tree")

		By("Every SnapshotContent created by this run must carry the marker")
		contents, err := dyn.Resource(snapContentGVR).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		newContents := 0
		for i := range contents.Items {
			c := &contents.Items[i]
			if _, existed := baseline[c.GetName()]; existed {
				continue
			}
			newContents++
			Expect(isProtected(c)).To(BeTrue(), "SnapshotContent %s must be delete-protected", c.GetName())
		}
		Expect(newContents).To(BeNumerically(">=", 1), "expected at least one new SnapshotContent")
	})

	// ── Group B: delete-protection guard ──

	It("break-glass admits DELETE of a protected SnapshotContent while the marker persists", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		// Pick a protected child SnapshotContent (owned by another content), so deleting it does not tear
		// down an entire root tree we still assert on later.
		contents, err := dyn.Resource(snapContentGVR).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		var target string
		for i := range contents.Items {
			c := &contents.Items[i]
			if _, existed := baseline[c.GetName()]; existed || !isProtected(c) {
				continue
			}
			owners := c.GetOwnerReferences()
			if len(owners) > 0 { // a child content (has a content owner)
				target = c.GetName()
				break
			}
		}
		if target == "" {
			Skip("no protected child SnapshotContent available for the break-glass check")
		}

		By("Stamping break-glass and issuing DELETE")
		Expect(annotateAllowDelete(ctx, dyn, snapContentGVR, "", target)).To(Succeed())
		// Marker must still be present right after stamping the annotation.
		cur, err := get(ctx, dyn, snapContentGVR, "", target)
		Expect(err).NotTo(HaveOccurred())
		Expect(isProtected(cur)).To(BeTrue(), "break-glass must not remove the marker")
		Expect(dyn.Resource(snapContentGVR).Delete(ctx, target, metav1.DeleteOptions{})).
			To(Succeed(), "break-glass DELETE of a protected object must be admitted")
	})

	It("denies direct DELETE and marker mutation of protected objects (requires "+envDeleteGuard+"=true / enforcement=Deny)", func() {
		if !guardDeny {
			Skip(envDeleteGuard + " not set: the delete-guard VAP must be switched to enforcement=Deny for Forbidden assertions")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// A protected root SnapshotContent (no content owner) that is not annotated for break-glass.
		contents, err := dyn.Resource(snapContentGVR).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		var target string
		for i := range contents.Items {
			c := &contents.Items[i]
			if _, existed := baseline[c.GetName()]; existed || !isProtected(c) {
				continue
			}
			if c.GetAnnotations()[breakGlassAnnotation] == "true" {
				continue
			}
			target = c.GetName()
			break
		}
		Expect(target).NotTo(BeEmpty(), "expected a protected, non-break-glass SnapshotContent")

		By("Direct DELETE must be Forbidden")
		err = dyn.Resource(snapContentGVR).Delete(ctx, target, metav1.DeleteOptions{})
		Expect(err).To(HaveOccurred())
		Expect(isGuardDenial(err)).To(BeTrue(), "DELETE must be denied by the delete-guard VAP: %v", err)

		By("Removing the marker via UPDATE must be Forbidden")
		cur, err := get(ctx, dyn, snapContentGVR, "", target)
		Expect(err).NotTo(HaveOccurred())
		labels := cur.GetLabels()
		delete(labels, deleteProtectedLabel)
		cur.SetLabels(labels)
		_, err = dyn.Resource(snapContentGVR).Update(ctx, cur, metav1.UpdateOptions{})
		Expect(err).To(HaveOccurred())
		Expect(isGuardDenial(err)).To(BeTrue(), "marker removal must be denied by the delete-guard VAP: %v", err)
	})

	It("allows deleting the unmarked user roots, cascading their trees", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		Expect(dyn.Resource(vmSnapGVR).Namespace(ns).Delete(ctx, treeSnap, metav1.DeleteOptions{})).To(Succeed())
		Expect(dyn.Resource(vdSnapGVR).Namespace(ns).Delete(ctx, leafSnap, metav1.DeleteOptions{})).To(Succeed())

		By("All demo snapshots in the namespace disappear (no finalizer hang)")
		Eventually(func(g Gomega) {
			vds, err := dyn.Resource(vdSnapGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			vms, err := dyn.Resource(vmSnapGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(vds.Items) + len(vms.Items)).To(BeZero())
		}).WithTimeout(3 * time.Minute).WithPolling(4 * time.Second).Should(Succeed())
	})
})
