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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

const (
	demoGroup   = "demo.state-snapshotter.deckhouse.io"
	demoVersion = "v1alpha1"
)

var (
	vdSnapshotGVR = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualdisksnapshots"}
	vmSnapshotGVR = schema.GroupVersionResource{Group: demoGroup, Version: demoVersion, Resource: "demovirtualmachinesnapshots"}

	// snapshotContent is cluster-scoped (state-snapshotter.deckhouse.io/v1alpha1).
	snapshotContentGVR = schema.GroupVersionResource{Group: "state-snapshotter.deckhouse.io", Version: "v1alpha1", Resource: "snapshotcontents"}
)

// scenario configuration (overridable via env for convenience).
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

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

// newSnapshot builds an unstructured demo snapshot referencing the given source.
func newSnapshot(kind, name, namespace, sourceKind, sourceName string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": demoGroup + "/" + demoVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"sourceRef": map[string]interface{}{
				"apiVersion": demoGroup + "/" + demoVersion,
				"kind":       sourceKind,
				"name":       sourceName,
			},
		},
	})
	return u
}

// condStatus returns the status of the given condition type ("True"/"False"/"" if absent).
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

func readyStatus(u *unstructured.Unstructured) string { return condStatus(u, "Ready") }

// hop records the first wall-clock offset (from start) at which a milestone was observed True.
type timeline struct {
	start time.Time
	order []string
	seen  map[string]time.Duration
}

func newTimeline(start time.Time) *timeline {
	return &timeline{start: start, seen: map[string]time.Duration{}}
}

func (tl *timeline) mark(name string) {
	if _, ok := tl.seen[name]; ok {
		return
	}
	tl.seen[name] = time.Since(tl.start)
	tl.order = append(tl.order, name)
}

// markTrueConditions marks "<prefix>/<condType>" for every condition currently True.
func (tl *timeline) markTrueConditions(u *unstructured.Unstructured, prefix string) {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return
	}
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if s, _ := m["status"].(string); s == "True" {
			if t, _ := m["type"].(string); t != "" {
				tl.mark(prefix + "/" + t)
			}
		}
	}
}

func (tl *timeline) print(w func(format string, args ...interface{})) {
	w("    ── timeline (first-seen offset from create) ──\n")
	for _, name := range tl.order {
		w("      %7.2fs  %s\n", tl.seen[name].Seconds(), name)
	}
}

// trackTreeReady polls the VM snapshot and its bound SnapshotContent, recording the first-seen
// offset of each milestone, until the VM snapshot's Ready condition is True (or the context expires).
func trackTreeReady(ctx context.Context, dyn dynamic.Interface, ns, name string, start time.Time) (*timeline, error) {
	tl := newTimeline(start)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		vms, err := dyn.Resource(vmSnapshotGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			tl.markTrueConditions(vms, "vms")
			if cn, ok, _ := unstructured.NestedString(vms.Object, "status", "boundSnapshotContentName"); ok && cn != "" {
				tl.mark("boundSnapshotContentName=" + cn)
				if content, cerr := dyn.Resource(snapshotContentGVR).Get(ctx, cn, metav1.GetOptions{}); cerr == nil {
					tl.markTrueConditions(content, "content")
				}
			}
			if condStatus(vms, "Ready") == "True" {
				tl.mark("vms/Ready(final)")
				return tl, nil
			}
		}
		select {
		case <-ctx.Done():
			return tl, fmt.Errorf("timeout waiting for %s Ready: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitReady polls the object until its Ready condition is True or the context expires,
// returning the elapsed time to Ready.
func waitReady(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, ns, name string, start time.Time) (time.Duration, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		obj, err := dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil && readyStatus(obj) == "True" {
			return time.Since(start), nil
		}
		select {
		case <-ctx.Done():
			return time.Since(start), fmt.Errorf("timeout waiting for %s/%s Ready: %w", gvr.Resource, name, ctx.Err())
		case <-ticker.C:
		}
	}
}

// Our scenario: standard scheme = a standalone VirtualDisk snapshot (leaf) plus a
// VirtualMachine snapshot (tree: VM -> disk-vm). We create both, measure time-to-Ready,
// and clean up. Sources (disk-standalone, vm-1) must already exist in the namespace.
var _ = Describe("Snapshot creation latency", Ordered, func() {
	var (
		res       *cluster.TestClusterResources
		dyn       dynamic.Interface
		namespace = envOr("SNAP_NAMESPACE", "ss-demo")
		diskName  = envOr("SNAP_DISK_SOURCE", "disk-standalone")
		vmName    = envOr("SNAP_VM_SOURCE", "vm-1")
		runID     = fmt.Sprintf("%d", time.Now().Unix())
		vdsName   = "vds-lat-" + runID
		vmsName   = "vms-lat-" + runID

		// names of trees created by the scalability spec, for cleanup.
		scaleTrees []string
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		By("Deleting snapshots", func() {
			if dyn != nil {
				_ = dyn.Resource(vdSnapshotGVR).Namespace(namespace).Delete(ctx, vdsName, metav1.DeleteOptions{})
				_ = dyn.Resource(vmSnapshotGVR).Namespace(namespace).Delete(ctx, vmsName, metav1.DeleteOptions{})
				for _, n := range scaleTrees {
					_ = dyn.Resource(vmSnapshotGVR).Namespace(namespace).Delete(ctx, n, metav1.DeleteOptions{})
				}
			}
		})
		By("Closing tunnel and SSH connection", func() {
			if res.TunnelInfo != nil && res.TunnelInfo.StopFunc != nil {
				res.TunnelInfo.StopFunc()
			}
			if res.SSHClient != nil {
				res.SSHClient.Close()
			}
		})
	})

	It("standalone VirtualDisk snapshot becomes Ready (leaf)", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		snap := newSnapshot("DemoVirtualDiskSnapshot", vdsName, namespace, "DemoVirtualDisk", diskName)
		start := time.Now()
		_, err := dyn.Resource(vdSnapshotGVR).Namespace(namespace).Create(ctx, snap, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to create DemoVirtualDiskSnapshot")

		elapsed, err := waitReady(ctx, dyn, vdSnapshotGVR, namespace, vdsName, start)
		Expect(err).NotTo(HaveOccurred(), "leaf snapshot did not become Ready in time")
		GinkgoWriter.Printf("    ⏱️  LEAF (VirtualDisk %s) Ready in %.2fs\n", diskName, elapsed.Seconds())
	})

	It("VirtualMachine snapshot becomes Ready (tree)", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		snap := newSnapshot("DemoVirtualMachineSnapshot", vmsName, namespace, "DemoVirtualMachine", vmName)
		start := time.Now()
		_, err := dyn.Resource(vmSnapshotGVR).Namespace(namespace).Create(ctx, snap, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to create DemoVirtualMachineSnapshot")

		tl, err := trackTreeReady(ctx, dyn, namespace, vmsName, start)
		Expect(err).NotTo(HaveOccurred(), "tree snapshot did not become Ready in time")
		GinkgoWriter.Printf("    ⏱️  TREE (VirtualMachine %s) Ready in %.2fs\n", vmName, tl.seen["vms/Ready(final)"].Seconds())
		tl.print(GinkgoWriter.Printf)
	})

	It("scales: N VirtualMachine snapshot trees become Ready concurrently", func() {
		n, err := strconv.Atoi(envOr("SNAP_TREES", "5"))
		if err != nil || n <= 0 {
			n = 5
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		GinkgoWriter.Printf("    ▶️ Creating %d VM snapshot trees of %q concurrently...\n", n, vmName)

		type result struct {
			name    string
			elapsed time.Duration
			err     error
		}
		results := make([]result, n)
		var wg sync.WaitGroup
		wallStart := time.Now()

		for i := 0; i < n; i++ {
			name := fmt.Sprintf("vms-scale-%s-%d", runID, i)
			scaleTrees = append(scaleTrees, name)
			wg.Add(1)
			go func(idx int, nm string) {
				defer GinkgoRecover()
				defer wg.Done()
				snap := newSnapshot("DemoVirtualMachineSnapshot", nm, namespace, "DemoVirtualMachine", vmName)
				start := time.Now()
				if _, cerr := dyn.Resource(vmSnapshotGVR).Namespace(namespace).Create(ctx, snap, metav1.CreateOptions{}); cerr != nil {
					results[idx] = result{name: nm, err: cerr}
					return
				}
				elapsed, werr := waitReady(ctx, dyn, vmSnapshotGVR, namespace, nm, start)
				results[idx] = result{name: nm, elapsed: elapsed, err: werr}
			}(i, name)
		}
		wg.Wait()
		wallElapsed := time.Since(wallStart)

		var durs []time.Duration
		var failed int
		for _, r := range results {
			if r.err != nil {
				failed++
				GinkgoWriter.Printf("      ❌ %s: %v\n", r.name, r.err)
				continue
			}
			durs = append(durs, r.elapsed)
		}
		Expect(failed).To(BeZero(), "some trees did not become Ready")

		printStats(GinkgoWriter.Printf, n, wallElapsed, durs)
	})
})

// printStats prints latency distribution for the scalability run.
func printStats(w func(format string, args ...interface{}), n int, wall time.Duration, durs []time.Duration) {
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	sec := func(d time.Duration) float64 { return d.Seconds() }
	pct := func(p float64) time.Duration {
		if len(durs) == 0 {
			return 0
		}
		idx := int(p * float64(len(durs)-1))
		return durs[idx]
	}
	var sum time.Duration
	for _, d := range durs {
		sum += d
	}
	avg := time.Duration(0)
	if len(durs) > 0 {
		avg = sum / time.Duration(len(durs))
	}
	w("    ── scalability: %d trees concurrently ──\n", n)
	w("      wall (all Ready): %.2fs\n", sec(wall))
	if len(durs) > 0 {
		w("      per-tree Ready: min=%.2fs  p50=%.2fs  avg=%.2fs  p90=%.2fs  max=%.2fs\n",
			sec(durs[0]), sec(pct(0.5)), sec(avg), sec(pct(0.9)), sec(durs[len(durs)-1]))
	}
}
