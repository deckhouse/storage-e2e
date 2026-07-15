/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package conformance verifies that a cluster provider honors the pkg/e2e
// capability contracts (NodeExecutor, DiskManager) against a live cluster.
// Every provider must pass it — that is what keeps the semantics of the modes
// (dvp, commander, future ones) from silently diverging.
//
// It runs against real infrastructure, so it is invoked explicitly, e.g. from
// a dedicated suite or a plain test:
//
//	cl, _ := e2e.Connect(ctx)
//	defer cl.Close(ctx)
//	report := conformance.Verify(ctx, cl, conformance.Config{NodeName: "worker-0"})
//	if err := report.Err(); err != nil { ... }
package conformance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/deckhouse/storage-e2e/pkg/e2e"
)

// Config parametrizes a conformance run.
type Config struct {
	// NodeName is the node the checks run against. When empty, the first
	// worker (non-control-plane) node of the cluster is used.
	NodeName string
}

// Result is the outcome of one conformance check.
type Result struct {
	Name string
	Err  error
}

// Report aggregates the results of a conformance run.
type Report struct {
	Results []Result
}

// Err joins the errors of all failed checks, or returns nil when every check
// passed.
func (r *Report) Err() error {
	var errs []error
	for _, res := range r.Results {
		if res.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", res.Name, res.Err))
		}
	}
	return errors.Join(errs...)
}

// Verify runs all conformance checks against the connected cluster and
// returns a per-check report. It does not stop on the first failure.
func Verify(ctx context.Context, cluster *e2e.Cluster, cfg Config) *Report {
	report := &Report{}
	record := func(name string, err error) {
		report.Results = append(report.Results, Result{Name: name, Err: err})
	}

	nodeName := cfg.NodeName
	if nodeName == "" {
		resolved, err := pickWorkerNode(ctx, cluster)
		if err != nil {
			record("resolve target node", err)
			return report
		}
		nodeName = resolved
	}

	record("node executor", VerifyNodeExecutor(ctx, cluster.Nodes(), nodeName))
	record("disk manager", VerifyDiskManager(ctx, cluster, nodeName))
	return report
}

// VerifyNodeExecutor checks the NodeExecutor contract on the given node:
// stdout/stderr are captured separately, the exit code of a completed command
// is reported without an error, and sudo is available non-interactively.
func VerifyNodeExecutor(ctx context.Context, nodes e2e.NodeExecutor, nodeName string) error {
	res, err := nodes.Exec(ctx, nodeName, "echo -n conformance-stdout; echo -n conformance-stderr 1>&2")
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if got := string(res.Stdout); got != "conformance-stdout" {
		return fmt.Errorf("stdout mismatch: %q", got)
	}
	if got := string(res.Stderr); got != "conformance-stderr" {
		return fmt.Errorf("stderr mismatch: %q", got)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("unexpected exit code %d", res.ExitCode)
	}

	res, err = nodes.Exec(ctx, nodeName, "exit 42")
	if err != nil {
		return fmt.Errorf("non-zero exit must not be an error, got: %w", err)
	}
	if res.ExitCode != 42 {
		return fmt.Errorf("exit code not propagated: got %d, want 42", res.ExitCode)
	}

	res, err = nodes.Exec(ctx, nodeName, "sudo -n true")
	if err != nil {
		return fmt.Errorf("sudo check: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("passwordless sudo unavailable (exit %d): %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// devicePollInterval paces the node-side lsblk checks after attach/detach:
// the DiskManager already waits for the attachment state, this only absorbs
// the guest OS noticing the hotplug.
const devicePollInterval = 5 * time.Second

const cleanupTimeout = 5 * time.Minute

// VerifyDiskManager checks the DiskManager contract on the given node with a
// full disk lifecycle: create, attach (a new block device must appear in the
// node's lsblk), detach (the device must disappear), delete. A provider that
// does not support disk management passes too, as long as every operation
// consistently reports ErrDisksUnsupported.
func VerifyDiskManager(ctx context.Context, cluster *e2e.Cluster, nodeName string) error {
	return verifyDiskLifecycle(ctx, cluster.Disks(), cluster.Nodes(), nodeName)
}

// verifyDiskLifecycle is the testable core of VerifyDiskManager: it depends
// only on the capability contracts, so unit tests drive it with fakes.
func verifyDiskLifecycle(ctx context.Context, disks e2e.DiskManager, nodes e2e.NodeExecutor, nodeName string) error {
	diskName := fmt.Sprintf("conformance-disk-%s", rand.String(5))

	disk, err := disks.CreateDisk(ctx, e2e.DiskSpec{Name: diskName, Size: resource.MustParse("1Gi")})
	if errors.Is(err, e2e.ErrDisksUnsupported) {
		return verifyDisksUnsupportedStub(ctx, disks)
	}
	if err != nil {
		return fmt.Errorf("create disk: %w", err)
	}
	// Best-effort teardown for whatever the checks below leave behind.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = disks.DetachDisk(cleanupCtx, nodeName, diskName)
		_ = disks.DeleteDisk(cleanupCtx, diskName)
	}()
	if disk.Name != diskName {
		return fmt.Errorf("created disk name mismatch: got %q, want %q", disk.Name, diskName)
	}

	before, err := blockDeviceNames(ctx, nodes, nodeName)
	if err != nil {
		return fmt.Errorf("list node block devices: %w", err)
	}

	if err := disks.AttachDisk(ctx, nodeName, diskName); err != nil {
		return fmt.Errorf("attach disk: %w", err)
	}
	device, err := waitNewBlockDevice(ctx, nodes, nodeName, before)
	if err != nil {
		return fmt.Errorf("after attach: %w", err)
	}

	if err := disks.DetachDisk(ctx, nodeName, diskName); err != nil {
		return fmt.Errorf("detach disk: %w", err)
	}
	if err := waitBlockDeviceGone(ctx, nodes, nodeName, device); err != nil {
		return fmt.Errorf("after detach: %w", err)
	}

	if err := disks.DeleteDisk(ctx, diskName); err != nil {
		return fmt.Errorf("delete disk: %w", err)
	}
	return nil
}

// verifyDisksUnsupportedStub checks that a provider without disk support
// reports ErrDisksUnsupported from every operation, not just CreateDisk.
func verifyDisksUnsupportedStub(ctx context.Context, disks e2e.DiskManager) error {
	if err := disks.DeleteDisk(ctx, "conformance-none"); !errors.Is(err, e2e.ErrDisksUnsupported) {
		return fmt.Errorf("DeleteDisk on an unsupported provider: got %v, want ErrDisksUnsupported", err)
	}
	if err := disks.AttachDisk(ctx, "conformance-none", "conformance-none"); !errors.Is(err, e2e.ErrDisksUnsupported) {
		return fmt.Errorf("AttachDisk on an unsupported provider: got %v, want ErrDisksUnsupported", err)
	}
	if err := disks.DetachDisk(ctx, "conformance-none", "conformance-none"); !errors.Is(err, e2e.ErrDisksUnsupported) {
		return fmt.Errorf("DetachDisk on an unsupported provider: got %v, want ErrDisksUnsupported", err)
	}
	return nil
}

// blockDeviceNames returns the names of the node's whole block devices
// (lsblk TYPE=disk, partitions excluded).
func blockDeviceNames(ctx context.Context, nodes e2e.NodeExecutor, nodeName string) ([]string, error) {
	res, err := nodes.Exec(ctx, nodeName, "lsblk -dno NAME,TYPE")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("lsblk exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var names []string
	for line := range strings.Lines(string(res.Stdout)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "disk" {
			names = append(names, fields[0])
		}
	}
	return names, nil
}

// waitNewBlockDevice polls the node until exactly one block device beyond the
// before snapshot shows up and returns its name. Identifying the device by
// name (rather than comparing counts) keeps the check meaningful when other
// disk activity happens on the node concurrently — and lets the detach check
// wait for this specific device to disappear. More than one new device is an
// unattributable state and fails immediately.
func waitNewBlockDevice(ctx context.Context, nodes e2e.NodeExecutor, nodeName string, before []string) (string, error) {
	ticker := time.NewTicker(devicePollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		got, err := blockDeviceNames(ctx, nodes, nodeName)
		if err != nil {
			lastErr = err
		} else {
			var extra []string
			for _, name := range got {
				if !slices.Contains(before, name) {
					extra = append(extra, name)
				}
			}
			switch len(extra) {
			case 0:
				// keep polling
			case 1:
				return extra[0], nil
			default:
				return "", fmt.Errorf("%d new block devices appeared %v, cannot attribute the attached disk", len(extra), extra)
			}
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return "", fmt.Errorf("waiting for a new block device: %w (last error: %w)", ctx.Err(), lastErr)
			}
			return "", fmt.Errorf("no new block device appeared (before: %v): %w", before, ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitBlockDeviceGone polls the node until the named block device disappears.
func waitBlockDeviceGone(ctx context.Context, nodes e2e.NodeExecutor, nodeName, device string) error {
	ticker := time.NewTicker(devicePollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		got, err := blockDeviceNames(ctx, nodes, nodeName)
		if err != nil {
			lastErr = err
		} else if !slices.Contains(got, device) {
			return nil
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("waiting for block device %q to disappear: %w (last error: %w)", device, ctx.Err(), lastErr)
			}
			return fmt.Errorf("block device %q still present: %w", device, ctx.Err())
		case <-ticker.C:
		}
	}
}

func pickWorkerNode(ctx context.Context, cluster *e2e.Cluster) (string, error) {
	cs, err := cluster.Clientset()
	if err != nil {
		return "", err
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			continue
		}
		if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
			continue
		}
		return node.Name, nil
	}
	return "", fmt.Errorf("no worker nodes found")
}
