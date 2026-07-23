/*
Copyright 2025 Flant JSC

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

package snapshot_latency

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

// Canonical scalability benchmark: ONE namespace-wide root Snapshot over N INDEPENDENT "standard sets".
//
// A standard set (index i) is:
//   - vm-i               DemoVirtualMachine, owns disk-i (spec.virtualDiskName=disk-i)
//   - disk-i / pvc-i     vm-owned DemoVirtualDisk backed by its own PVC
//   - sdisk-i / spvc-i   standalone DemoVirtualDisk backed by its own PVC (not owned by any VM)
//
// Every set has its OWN PVCs, so the volume legs are independent VolumeSnapshots of distinct source
// volumes: there is NO CSI same-source serialization. This isolates scaling to namespace/tree SIZE, which
// is the real production shape (one namespace snapshot fanning out), unlike N parallel snapshots of the
// same object (a separate stress case that must not be the primary scalability signal).
//
// Run it for SNAP_SETS=1, 5, 10 and compare the single root Snapshot's time-to-Ready.

const (
	benchStorageClass = "local-thin"
	benchPVCSize      = "1Mi"
	benchBindImage    = "registry.k8s.io/pause:3.9"
)

var (
	coreV1 = "v1"

	pvcGVR       = schema.GroupVersionResource{Group: "", Version: coreV1, Resource: "persistentvolumeclaims"}
	podGVR       = schema.GroupVersionResource{Group: "", Version: coreV1, Resource: "pods"}
	nsGVR        = schema.GroupVersionResource{Group: "", Version: coreV1, Resource: "namespaces"}
	demoVMGVR    = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualmachines"}
	demoVDGVR    = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualdisks"}
	rootSnapGVR  = schema.GroupVersionResource{Group: "state-snapshotter.deckhouse.io", Version: "v1alpha1", Resource: "snapshots"}
	demoAPIVers  = demoGroup + "/" + demoVersion
	storageAPIVs = "state-snapshotter.deckhouse.io/v1alpha1"
)

func nsObject(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(coreV1)
	u.SetKind("Namespace")
	u.SetName(name)
	return u
}

func pvcObject(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": coreV1,
		"kind":       "PersistentVolumeClaim",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"accessModes":      []interface{}{"ReadWriteOnce"},
			"storageClassName": benchStorageClass,
			"resources":        map[string]interface{}{"requests": map[string]interface{}{"storage": benchPVCSize}},
		},
	}}
}

// bindPod mounts every PVC of a set so a WaitForFirstConsumer StorageClass binds them and keeps the
// volumes attached (pause runs forever) for the duration of the snapshot.
func bindPod(ns, name string, claims []string) *unstructured.Unstructured {
	mounts := make([]interface{}, 0, len(claims))
	volumes := make([]interface{}, 0, len(claims))
	for i, c := range claims {
		vn := fmt.Sprintf("v%d", i)
		mounts = append(mounts, map[string]interface{}{"name": vn, "mountPath": fmt.Sprintf("/data%d", i)})
		volumes = append(volumes, map[string]interface{}{"name": vn, "persistentVolumeClaim": map[string]interface{}{"claimName": c}})
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": coreV1,
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"restartPolicy": "Always",
			"containers":    []interface{}{map[string]interface{}{"name": "hold", "image": benchBindImage, "volumeMounts": mounts}},
			"volumes":       volumes,
		},
	}}
}

func demoVM(ns, name, diskName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": demoAPIVers,
		"kind":       "DemoVirtualMachine",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec":       map[string]interface{}{"virtualDiskName": diskName},
	}}
}

// demoVD is a blank DemoVirtualDisk that provisions its OWN PVC (named pvc) from size+storageClassName.
// Each disk therefore has an independent source volume (no CSI same-source serialization).
func demoVD(ns, name, pvc string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": demoAPIVers,
		"kind":       "DemoVirtualDisk",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec": map[string]interface{}{
			"persistentVolumeClaimName": pvc,
			"size":                      benchPVCSize,
			"storageClassName":          benchStorageClass,
		},
	}}
}

func rootSnapshot(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": storageAPIVs,
		"kind":       "Snapshot",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"spec":       map[string]interface{}{},
	}}
}

func createObj(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured) {
	var err error
	if ns == "" {
		_, err = dyn.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
	} else {
		_, err = dyn.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	}
	Expect(err).NotTo(HaveOccurred(), "create %s/%s", gvr.Resource, obj.GetName())
}

// waitAllPVCsBound blocks until every named PVC reports status.phase=Bound (setup, not measured).
func waitAllPVCsBound(ctx context.Context, dyn dynamic.Interface, ns string, names []string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		bound := 0
		for _, n := range names {
			p, err := dyn.Resource(pvcGVR).Namespace(ns).Get(ctx, n, metav1.GetOptions{})
			if err == nil {
				if ph, _, _ := unstructured.NestedString(p.Object, "status", "phase"); ph == "Bound" {
					bound++
				}
			}
		}
		if bound == len(names) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout: only %d/%d PVCs Bound: %w", bound, len(names), ctx.Err())
		case <-ticker.C:
		}
	}
}

// trackChildLeafReady polls all DemoVirtualDiskSnapshots in the namespace (the data-backed leaves of the
// tree) and records the first wall-clock offset at which each becomes Ready. The sorted offsets expose a
// staircase (serialized) vs a tight cluster (parallel) — the core scalability signal.
func trackChildLeafReady(ctx context.Context, dyn dynamic.Interface, ns string, start time.Time, done <-chan struct{}) map[string]time.Duration {
	seen := map[string]time.Duration{}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		lst, err := dyn.Resource(vdSnapshotGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			for i := range lst.Items {
				it := &lst.Items[i]
				if _, ok := seen[it.GetName()]; ok {
					continue
				}
				if condStatus(it, "Ready") == "True" {
					seen[it.GetName()] = time.Since(start)
				}
			}
		}
		select {
		case <-ctx.Done():
			return seen
		case <-done:
			return seen
		case <-ticker.C:
		}
	}
}

var _ = Describe("Namespace snapshot scalability (independent standard sets)", Ordered, func() {
	var (
		res       *cluster.TestClusterResources
		dyn       dynamic.Interface
		benchNS   string
		createdNS bool
	)

	BeforeAll(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		By("Connecting to existing cluster", func() {
			res = connectExisting(ctx)
			var err error
			dyn, err = dynamic.NewForConfig(res.Kubeconfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to build dynamic client")
		})
	})

	AfterAll(func() {
		if res == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if createdNS && dyn != nil {
			By("Deleting benchmark namespace", func() {
				_ = dyn.Resource(nsGVR).Delete(ctx, benchNS, metav1.DeleteOptions{})
			})
		}
		By("Closing tunnel and SSH connection", func() {
			if res.TunnelInfo != nil && res.TunnelInfo.StopFunc != nil {
				res.TunnelInfo.StopFunc()
			}
			if res.SSHClient != nil {
				res.SSHClient.Close()
			}
		})
	})

	It("one namespace snapshot over N independent standard sets becomes Ready", func() {
		n, err := strconv.Atoi(envOr("SNAP_SETS", "5"))
		if err != nil || n <= 0 {
			n = 5
		}
		runID := fmt.Sprintf("%d", time.Now().Unix())
		benchNS = fmt.Sprintf("snap-bench-%d-%s", n, runID)

		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()

		By(fmt.Sprintf("Creating namespace %s with %d standard sets", benchNS, n), func() {
			createObj(ctx, dyn, nsGVR, "", nsObject(benchNS))
			createdNS = true

			var pvcNames []string
			for i := 0; i < n; i++ {
				pvc := fmt.Sprintf("pvc-%d", i)
				spvc := fmt.Sprintf("spvc-%d", i)
				pvcNames = append(pvcNames, pvc, spvc)
				// Disks provision the PVCs (blank); bind pod mounts them so a WaitForFirstConsumer SC binds.
				createObj(ctx, dyn, demoVDGVR, benchNS, demoVD(benchNS, fmt.Sprintf("disk-%d", i), pvc))
				createObj(ctx, dyn, demoVDGVR, benchNS, demoVD(benchNS, fmt.Sprintf("sdisk-%d", i), spvc))
				createObj(ctx, dyn, demoVMGVR, benchNS, demoVM(benchNS, fmt.Sprintf("vm-%d", i), fmt.Sprintf("disk-%d", i)))
				createObj(ctx, dyn, podGVR, benchNS, bindPod(benchNS, fmt.Sprintf("bind-%d", i), []string{pvc, spvc}))
			}

			By("Waiting for all PVCs to bind (setup, not measured)", func() {
				setupCtx, c := context.WithTimeout(ctx, 15*time.Minute)
				defer c()
				Expect(waitAllPVCsBound(setupCtx, dyn, benchNS, pvcNames)).NotTo(HaveOccurred())
			})
		})

		snapName := "bench-root"
		start := time.Now()
		trackCtx, trackCancel := context.WithCancel(ctx)
		leafCh := make(chan map[string]time.Duration, 1)
		go func() {
			defer GinkgoRecover()
			leafCh <- trackChildLeafReady(trackCtx, dyn, benchNS, start, trackCtx.Done())
		}()

		By("Creating the single namespace-wide root Snapshot and measuring time-to-Ready", func() {
			createObj(ctx, dyn, rootSnapGVR, benchNS, rootSnapshot(benchNS, snapName))
			tl, werr := trackRootReady(ctx, dyn, benchNS, snapName, start)
			trackCancel()
			leaves := <-leafCh
			Expect(werr).NotTo(HaveOccurred(), "root Snapshot did not become Ready in time")

			wall := tl.seen["snapshot/Ready(final)"]
			GinkgoWriter.Printf("\n    ══ SETS=%d ══\n", n)
			GinkgoWriter.Printf("      root Snapshot Ready in %.2fs (children in tree: %d)\n", wall.Seconds(), tl.childRefs)
			tl.print(GinkgoWriter.Printf)
			printLeafStaircase(GinkgoWriter.Printf, leaves)
		})
	})
})

// rootTimeline extends timeline with the observed child count of the root Snapshot.
type rootTimeline struct {
	*timeline
	childRefs int
}

func trackRootReady(ctx context.Context, dyn dynamic.Interface, ns, name string, start time.Time) (*rootTimeline, error) {
	rt := &rootTimeline{timeline: newTimeline(start)}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		snap, err := dyn.Resource(rootSnapGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			rt.markTrueConditions(snap, "snapshot")
			if refs, ok, _ := unstructured.NestedSlice(snap.Object, "status", "childrenSnapshotRefs"); ok {
				if len(refs) > rt.childRefs {
					rt.childRefs = len(refs)
				}
			}
			if condStatus(snap, "Ready") == "True" {
				rt.mark("snapshot/Ready(final)")
				return rt, nil
			}
		}
		select {
		case <-ctx.Done():
			return rt, fmt.Errorf("timeout waiting for root Snapshot %s Ready: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func printLeafStaircase(w func(format string, args ...interface{}), leaves map[string]time.Duration) {
	if len(leaves) == 0 {
		w("      leaf VirtualDiskSnapshot Ready offsets: (none observed)\n")
		return
	}
	offs := make([]float64, 0, len(leaves))
	for _, d := range leaves {
		offs = append(offs, d.Seconds())
	}
	sort.Float64s(offs)
	w("      leaf VirtualDiskSnapshot Ready offsets (s, sorted; flat=parallel, staircase=serialized):\n        %v\n", offs)
}
