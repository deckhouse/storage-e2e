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
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

// This spec answers a single question: for one namespace-wide root Snapshot over N independent standard
// sets, where does the time go? It splits the two suspected bottlenecks the wall-clock benchmark conflates:
//
//   leaf/data path : VCR created -> CSI VolumeSnapshotContent readyToUse -> VCR Ready -> leaf content Ready
//   aggregation    : all child contents Ready -> root ChildrenSnapshotReady -> root ManifestsArchived -> root Ready
//
// Hypotheses it discriminates:
//   A. CSI VSC readyToUse itself is a staircase           -> storage/CSI throughput.
//   B. readyToUse flat but VCR/leaf-content Ready staircase -> foundation controller/client.
//   C. all child contents Ready early, root Ready late     -> state-snapshotter subtree aggregation/latch.
//
// Gated on SNAP_TRACE (set SNAP_TRACE=1). SNAP_SETS controls N (default 10).

var (
	// VolumeCaptureRequest (storage-foundation), namespaced.
	vcrGVR = schema.GroupVersionResource{Group: "storage.deckhouse.io", Version: "v1alpha1", Resource: "volumecapturerequests"}
	// CSI VolumeSnapshotContent, cluster-scoped.
	csiVSCGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}
	// ManifestCaptureRequest (state-snapshotter), namespaced; ManifestCheckpoint, cluster-scoped.
	mcrGVR = schema.GroupVersionResource{Group: "state-snapshotter.deckhouse.io", Version: "v1alpha1", Resource: "manifestcapturerequests"}
	mcpGVR = schema.GroupVersionResource{Group: "state-snapshotter.deckhouse.io", Version: "v1alpha1", Resource: "manifestcheckpoints"}
)

// firstSeen records the first wall-clock offset (from start) each key crossed its threshold.
type firstSeen struct {
	start time.Time
	at    map[string]time.Duration
}

func newFirstSeen(start time.Time) *firstSeen {
	return &firstSeen{start: start, at: map[string]time.Duration{}}
}

func (f *firstSeen) mark(key string) {
	if _, ok := f.at[key]; !ok {
		f.at[key] = time.Since(f.start)
	}
}

// sortedOffsets returns the offsets whose key has the given prefix, sorted ascending (seconds).
func (f *firstSeen) sortedOffsets(prefix string) []float64 {
	out := []float64{}
	for k, d := range f.at {
		if strings.HasPrefix(k, prefix) {
			out = append(out, d.Seconds())
		}
	}
	sort.Float64s(out)
	return out
}

// markTrueConds marks "<obj>|<condType>" for every currently-True condition of u.
func (f *firstSeen) markTrueConds(u *unstructured.Unstructured, obj string) {
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
				f.mark(obj + "|" + t)
			}
		}
	}
}

var _ = Describe("TRACE namespace snapshot per-object decomposition", Ordered, func() {
	var (
		res       *cluster.TestClusterResources
		dyn       dynamic.Interface
		benchNS   string
		createdNS bool
	)

	BeforeAll(func() {
		if strings.TrimSpace(os.Getenv("SNAP_TRACE")) == "" {
			Skip("SNAP_TRACE not set; skipping per-object trace decomposition")
		}
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

	It("decomposes leaf vs aggregation phases for SETS sets", func() {
		n, err := strconv.Atoi(envOr("SNAP_SETS", "10"))
		if err != nil || n <= 0 {
			n = 10
		}
		runID := fmt.Sprintf("%d", time.Now().Unix())
		benchNS = fmt.Sprintf("snap-trace-%d-%s", n, runID)

		ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
		defer cancel()

		By(fmt.Sprintf("Creating namespace %s with %d standard sets", benchNS, n), func() {
			createObj(ctx, dyn, nsGVR, "", nsObject(benchNS))
			createdNS = true
			var pvcNames []string
			for i := 0; i < n; i++ {
				pvc := fmt.Sprintf("pvc-%d", i)
				spvc := fmt.Sprintf("spvc-%d", i)
				pvcNames = append(pvcNames, pvc, spvc)
				createObj(ctx, dyn, demoVDGVR, benchNS, demoVD(benchNS, fmt.Sprintf("disk-%d", i), pvc))
				createObj(ctx, dyn, demoVDGVR, benchNS, demoVD(benchNS, fmt.Sprintf("sdisk-%d", i), spvc))
				createObj(ctx, dyn, demoVMGVR, benchNS, demoVM(benchNS, fmt.Sprintf("vm-%d", i), fmt.Sprintf("disk-%d", i)))
				createObj(ctx, dyn, podGVR, benchNS, bindPod(benchNS, fmt.Sprintf("bind-%d", i), []string{pvc, spvc}))
			}
			By("Waiting for all PVCs to bind (setup, not measured)", func() {
				setupCtx, c := context.WithTimeout(ctx, 5*time.Minute)
				defer c()
				Expect(waitAllPVCsBound(setupCtx, dyn, benchNS, pvcNames)).NotTo(HaveOccurred())
			})
		})

		// Baseline of pre-existing CSI VolumeSnapshotContents and storage SnapshotContents so we only
		// attribute NEW ones (this run's subtree) — the tree fans out as cluster-scoped SnapshotContents,
		// not namespaced child Snapshots, so we cannot reach them via the namespaced Snapshot list.
		baselineVSC := map[string]bool{}
		if lst, lerr := dyn.Resource(csiVSCGVR).List(ctx, metav1.ListOptions{}); lerr == nil {
			for i := range lst.Items {
				baselineVSC[lst.Items[i].GetName()] = true
			}
		}
		baselineSC := map[string]bool{}
		if lst, lerr := dyn.Resource(snapshotContentGVR).List(ctx, metav1.ListOptions{}); lerr == nil {
			for i := range lst.Items {
				baselineSC[lst.Items[i].GetName()] = true
			}
		}

		fs := newFirstSeen(time.Now())
		start := fs.start
		// Root manifest-leg linkage (T-manifest): root content -> its ManifestCheckpoint -> its ManifestCaptureRequest.
		var rootContentName, rootMCPName, rootMCRName string
		var rootMCPObjects int64
		var rootMCPChunks int
		// Content-side lag probe: per-content manifest checkpoint name and leaf-ness so the decomposition can
		// answer, for each SnapshotContent, whether it flips Ready long after BOTH its inputs (its MCP Ready
		// and the volume artifacts) are ready = wake-up/queue/starvation, vs the input itself being late.
		contentMCP := map[string]string{}
		contentLeaf := map[string]bool{}
		createObj(ctx, dyn, rootSnapGVR, benchNS, rootSnapshot(benchNS, "trace-root"))

		By("Polling the subtree until the root Snapshot is Ready", func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				// Root Snapshot (namespaced): per-condition offsets.
				if s, e := dyn.Resource(rootSnapGVR).Namespace(benchNS).Get(ctx, "trace-root", metav1.GetOptions{}); e == nil {
					fs.markTrueConds(s, "ROOT")
				}
				// The whole subtree of cluster-scoped SnapshotContents created by this run (baseline diff):
				// per-content created + condition offsets (VolumesReady/ManifestsArchived/Ready/...).
				if lst, e := dyn.Resource(snapshotContentGVR).List(ctx, metav1.ListOptions{}); e == nil {
					for i := range lst.Items {
						c := &lst.Items[i]
						if baselineSC[c.GetName()] {
							continue
						}
						fs.markCreated("contentCreated|"+c.GetName(), c, start)
						fs.markTrueConds(c, "content/"+c.GetName())
						// Root content is "ns-<uid>" (children are "nss-child-..."). Capture its
						// published ManifestCheckpoint name so we can time the manifest leg.
						name := c.GetName()
						if mcp, ok, _ := unstructured.NestedString(c.Object, "status", "manifestCheckpointName"); ok && mcp != "" {
							contentMCP[name] = mcp
						}
						kids, _, _ := unstructured.NestedSlice(c.Object, "status", "childrenSnapshotContentRefs")
						contentLeaf[name] = len(kids) == 0
						if strings.HasPrefix(name, "ns-") && !strings.HasPrefix(name, "nss-") {
							rootContentName = name
							if m := contentMCP[name]; m != "" {
								rootMCPName = m
							}
						}
					}
				}
				// VolumeCaptureRequests targeting this namespace: created + Ready offsets.
				if vcrs, e := dyn.Resource(vcrGVR).List(ctx, metav1.ListOptions{}); e == nil {
					for i := range vcrs.Items {
						v := &vcrs.Items[i]
						if !vcrTargetsNamespace(v, benchNS) {
							continue
						}
						fs.markCreated("vcrCreated|"+v.GetName(), v, start)
						if condStatus(v, "Ready") == "True" {
							fs.mark("vcrReady|" + v.GetName())
						}
					}
				}
				// New CSI VolumeSnapshotContents: readyToUse offsets.
				if lst, e := dyn.Resource(csiVSCGVR).List(ctx, metav1.ListOptions{}); e == nil {
					for i := range lst.Items {
						c := &lst.Items[i]
						if baselineVSC[c.GetName()] {
							continue
						}
						fs.markCreated("vscCreated|"+c.GetName(), c, start)
						if rtu, ok, _ := unstructured.NestedBool(c.Object, "status", "readyToUse"); ok && rtu {
							fs.mark("vscReady|" + c.GetName())
						}
					}
				}

				// Per-content manifest leg: mark every ManifestCheckpoint's Ready offset (one list per tick,
				// cluster-scoped) so the content-side probe can pair each content with its own MCP Ready.
				if mcps, e := dyn.Resource(mcpGVR).List(ctx, metav1.ListOptions{}); e == nil {
					for i := range mcps.Items {
						m := &mcps.Items[i]
						if condStatus(m, "Ready") == "True" {
							fs.mark("mcp|" + m.GetName())
						}
					}
				}

				// Root manifest leg (T-manifest): MCP created/Ready + totalObjects/chunks, and its MCR
				// created/Ready. This is the namespace-wide manifest capture that the root content waits on.
				if rootMCPName != "" {
					if mcp, e := dyn.Resource(mcpGVR).Get(ctx, rootMCPName, metav1.GetOptions{}); e == nil {
						fs.markCreated("ROOT|MCPCreated", mcp, start)
						if condStatus(mcp, "Ready") == "True" {
							fs.mark("ROOT|MCPReady")
						}
						if to, ok, _ := unstructured.NestedInt64(mcp.Object, "status", "totalObjects"); ok && to > rootMCPObjects {
							rootMCPObjects = to
						}
						if chunks, ok, _ := unstructured.NestedSlice(mcp.Object, "status", "chunks"); ok && len(chunks) > rootMCPChunks {
							rootMCPChunks = len(chunks)
						}
						if ref, ok, _ := unstructured.NestedString(mcp.Object, "spec", "manifestCaptureRequestRef", "name"); ok && ref != "" {
							rootMCRName = ref
						}
					}
				}
				if rootMCRName != "" {
					if mcr, e := dyn.Resource(mcrGVR).Namespace(benchNS).Get(ctx, rootMCRName, metav1.GetOptions{}); e == nil {
						fs.markCreated("ROOT|MCRCreated", mcr, start)
						if condStatus(mcr, "Ready") == "True" {
							fs.mark("ROOT|MCRReady")
						}
					}
				}

				if _, done := fs.at["ROOT|Ready"]; done {
					return
				}
				select {
				case <-ctx.Done():
					Fail(fmt.Sprintf("root Snapshot did not become Ready in time: %v", ctx.Err()))
				case <-ticker.C:
				}
			}
		})

		printDecomposition(GinkgoWriter.Printf, n, fs)
		printManifestLeg(GinkgoWriter.Printf, fs, rootContentName, rootMCPName, rootMCRName, rootMCPObjects, rootMCPChunks)
		printContentSideLag(GinkgoWriter.Printf, fs, contentMCP, contentLeaf)
	})
})

// printContentSideLag answers the content-side question: after BOTH inputs of a SnapshotContent are ready
// (its own ManifestCheckpoint Ready for the manifest leg, and the volume artifacts for the volume leg), how
// long until the content flips its own legs and Ready? A large gap = wake-up/queue/starvation on the
// content controller; a late input = capture (MCP) or foundation/CSI (VCR) instead. The content type by
// design carries no VCR back-reference, so the volume leg is compared against the global VCR-Ready
// waterline (all volume artifacts ready by then) rather than a per-content VCR.
func printContentSideLag(w func(format string, args ...interface{}), fs *firstSeen, contentMCP map[string]string, contentLeaf map[string]bool) {
	w("\n    ══════════ content-side lag (per SnapshotContent) ══════════\n")
	volWaterline := lastOf(fs.sortedOffsets("vcrReady|"))
	w("      volume waterline (max VCR Ready, all volume artifacts ready by) = %.2fs\n", volWaterline)

	type row struct {
		name                                 string
		leaf                                 bool
		created, mcpReady, volR, manR, ready float64
	}
	var rows []row
	for key, d := range fs.at {
		if !strings.HasPrefix(key, "content/") || !strings.HasSuffix(key, "|Ready") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "content/"), "|Ready")
		r := row{name: name, leaf: contentLeaf[name], ready: d.Seconds()}
		r.created = offSec(fs, "contentCreated|"+name)
		r.volR = offSec(fs, "content/"+name+"|VolumesReady")
		r.manR = offSec(fs, "content/"+name+"|ManifestsReady")
		if mcp := contentMCP[name]; mcp != "" {
			r.mcpReady = offSec(fs, "mcp|"+mcp)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ready > rows[j].ready })

	w("      %-7s %-4s %10s %10s %10s %13s  %s\n", "Ready", "leg", "VolReady", "ManReady", "MCPReady", "mObserveLag", "name")
	for i := 0; i < len(rows) && i < 12; i++ {
		r := rows[i]
		leg := "M"
		if r.volR > r.manR {
			leg = "V"
		}
		w("      %6s %-4s %10s %10s %10s %13s  %s (leaf=%v)\n",
			fmtSec(r.ready), leg, fmtSec(r.volR), fmtSec(r.manR), fmtSec(r.mcpReady), fmtSec(r.manR-r.mcpReady), shortName(r.name), r.leaf)
	}

	if len(rows) > 0 {
		top := rows[0]
		w("    ── verdict (slowest content) ──\n")
		if top.volR > top.manR {
			w("      %s: straggler leg = VOLUME; VolumesReady @ %.2fs vs waterline %.2fs (volume observe lag = %.2fs)\n",
				shortName(top.name), top.volR, volWaterline, top.volR-volWaterline)
			w("      => VolumesReady >> waterline means the content was not woken after its VCR was ready (volume-leg wake-up/starvation); else foundation/CSI.\n")
		} else {
			w("      %s: straggler leg = MANIFEST; ManifestsReady @ %.2fs vs its MCP Ready @ %.2fs (manifest observe lag = %.2fs)\n",
				shortName(top.name), top.manR, top.mcpReady, top.manR-top.mcpReady)
			w("      => MCP Ready early but ManifestsReady late means wake-up/queue/starvation (content-side); MCP Ready itself late means capture.\n")
		}
	}
	w("    ═══════════════════════════════════════════════\n")
}

// offSec returns the recorded offset in seconds, or 0 if the key was never marked (treated as "missing").
func offSec(fs *firstSeen, key string) float64 {
	if d, ok := fs.at[key]; ok {
		return d.Seconds()
	}
	return 0
}

// fmtSec renders an offset, printing a dash for a missing (0) value so the table does not imply t=0.
func fmtSec(x float64) string {
	if x == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2fs", x)
}

func shortName(s string) string {
	if len(s) > 40 {
		return s[:37] + "..."
	}
	return s
}

// printManifestLeg answers T-manifest: is the root's manifest checkpoint produced late (namespace-wide
// capture genuinely slow) or ready early but observed late by the root content (reconcile cost / wake gap)?
func printManifestLeg(w func(format string, args ...interface{}), fs *firstSeen, rootContent, mcpName, mcrName string, objects int64, chunks int) {
	w("\n    ══════════ ROOT manifest leg (T-manifest) ══════════\n")
	w("      root content            : %s\n", rootContent)
	w("      root ManifestCheckpoint : %s (objects=%d chunks=%d)\n", mcpName, objects, chunks)
	w("      root ManifestCaptureReq : %s\n", mcrName)
	w("      1. MCR created          : %s\n", offOrDash(fs, "ROOT|MCRCreated"))
	w("      2. MCR Ready            : %s\n", offOrDash(fs, "ROOT|MCRReady"))
	w("      3. MCP created          : %s\n", offOrDash(fs, "ROOT|MCPCreated"))
	w("      4. MCP Ready            : %s\n", offOrDash(fs, "ROOT|MCPReady"))
	w("      5. root content ManifestsReady: %s\n", offOrDash(fs, "content/"+rootContent+"|ManifestsReady"))
	w("      6. ROOT Snapshot ManifestsReady: %s\n", offOrDash(fs, "ROOT|ManifestsReady"))
	mcpReady := fs.at["ROOT|MCPReady"].Seconds()
	contentMR := fs.at["content/"+rootContent+"|ManifestsReady"].Seconds()
	w("    ── verdict ──\n")
	w("      MCP Ready @ %.2fs  ->  root content sees ManifestsReady @ %.2fs  (observe lag = %.2fs)\n", mcpReady, contentMR, contentMR-mcpReady)
	w("      MCP-Ready-late (T-manifest) if MCP Ready ~= root Ready tail; observe-lag large => T-cost/T-dedup\n")
	w("    ═══════════════════════════════════════════════\n")
}

// markCreated records the creation offset (creationTimestamp - start) once, if positive-ish.
func (f *firstSeen) markCreated(key string, u *unstructured.Unstructured, start time.Time) {
	if _, ok := f.at[key]; ok {
		return
	}
	ts := u.GetCreationTimestamp()
	if ts.IsZero() {
		return
	}
	f.at[key] = ts.Time.Sub(start)
}

func vcrTargetsNamespace(v *unstructured.Unstructured, ns string) bool {
	targets, found, err := unstructured.NestedSlice(v.Object, "spec", "targets")
	if err != nil || !found {
		return false
	}
	for _, t := range targets {
		m, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		if tn, _ := m["namespace"].(string); tn == ns {
			return true
		}
	}
	return false
}

func printDecomposition(w func(format string, args ...interface{}), n int, fs *firstSeen) {
	w("\n    ══════════ TRACE decomposition: SETS=%d ══════════\n", n)

	// LEAF / DATA PATH (sorted offsets across all objects of each class).
	w("    ── leaf/data path (sorted offsets, s) ──\n")
	w("      VCR created      : %v\n", fs.sortedOffsets("vcrCreated|"))
	w("      CSI VSC created  : %v\n", fs.sortedOffsets("vscCreated|"))
	w("      CSI VSC readyToUse: %v\n", fs.sortedOffsets("vscReady|"))
	w("      VCR Ready        : %v\n", fs.sortedOffsets("vcrReady|"))

	// AGGREGATION PATH (root markers + all-content ManifestsArchived spread).
	w("    ── aggregation path (%d subtree SnapshotContents) ──\n", len(fs.sortedOffsets("contentCreated|")))
	w("      content created (sorted): %v\n", fs.sortedOffsets("contentCreated|"))
	w("      content VolumesReady (sorted): %v\n", contentCondOffsets(fs, "VolumesReady"))
	w("      content ManifestsReady (sorted): %v\n", contentCondOffsets(fs, "ManifestsReady"))
	w("      all child content Ready (sorted): %v\n", contentCondOffsets(fs, "Ready"))
	w("      all content ManifestsArchived (sorted): %v\n", contentCondOffsets(fs, "ManifestsArchived"))
	w("      ROOT ChildrenSnapshotReady: %s\n", offOrDash(fs, "ROOT|ChildrenSnapshotReady"))
	w("      ROOT ManifestsReady       : %s\n", offOrDash(fs, "ROOT|ManifestsReady"))
	w("      ROOT VolumesReady         : %s\n", offOrDash(fs, "ROOT|VolumesReady"))
	w("      ROOT ChildrenReady        : %s\n", offOrDash(fs, "ROOT|ChildrenReady"))
	w("      ROOT ManifestsArchived    : %s\n", offOrDash(fs, "ROOT|ManifestsArchived"))
	w("      ROOT Ready                : %s\n", offOrDash(fs, "ROOT|Ready"))

	// Phase split summary.
	leafLast := maxOffset(fs.sortedOffsets("vscReady|"), fs.sortedOffsets("vcrReady|"))
	childReadyLast := lastOf(contentCondOffsets(fs, "Ready"))
	root := fs.at["ROOT|Ready"].Seconds()
	csr := fs.at["ROOT|ChildrenSnapshotReady"].Seconds()
	w("    ── phase split ──\n")
	w("      leaf phase (data) ends ~%.2fs\n", leafLast)
	w("      all child content Ready by ~%.2fs\n", childReadyLast)
	w("      ROOT ChildrenSnapshotReady @ %.2fs\n", csr)
	w("      ROOT Ready @ %.2fs  => aggregation tail after ChildrenSnapshotReady = %.2fs\n", root, root-csr)
	w("    ── slowest 6 subtree contents (name @ Ready) ──\n")
	for _, ns := range slowestContents(fs, "Ready", 6) {
		w("      %s\n", ns)
	}
	w("    ═══════════════════════════════════════════════\n")
}

// contentCondOffsets collects the offset of the given condition across all content/* objects, sorted.
func contentCondOffsets(fs *firstSeen, cond string) []float64 {
	out := []float64{}
	suffix := "|" + cond
	for k, d := range fs.at {
		if strings.HasPrefix(k, "content/") && strings.HasSuffix(k, suffix) {
			out = append(out, d.Seconds())
		}
	}
	sort.Float64s(out)
	return out
}

// slowestContents returns up to k "name @ offset" strings for content/<name>|<cond>, slowest first.
func slowestContents(fs *firstSeen, cond string, k int) []string {
	type ns struct {
		name string
		off  float64
	}
	suffix := "|" + cond
	var items []ns
	for key, d := range fs.at {
		if strings.HasPrefix(key, "content/") && strings.HasSuffix(key, suffix) {
			name := strings.TrimSuffix(strings.TrimPrefix(key, "content/"), suffix)
			items = append(items, ns{name: name, off: d.Seconds()})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].off > items[j].off })
	out := []string{}
	for i := 0; i < len(items) && i < k; i++ {
		out = append(out, fmt.Sprintf("%.2fs  %s", items[i].off, items[i].name))
	}
	return out
}

func offOrDash(fs *firstSeen, key string) string {
	if d, ok := fs.at[key]; ok {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return "—"
}

func lastOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[len(xs)-1]
}

func maxOffset(a, b []float64) float64 {
	m := lastOf(a)
	if x := lastOf(b); x > m {
		m = x
	}
	return m
}
