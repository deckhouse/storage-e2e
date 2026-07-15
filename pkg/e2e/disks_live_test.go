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

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/deckhouse/storage-e2e/pkg/e2e"
	"github.com/deckhouse/storage-e2e/pkg/e2e/conformance"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// Live e2e tests for the DiskManager capability. They need a bootstrapped
// provider-managed cluster and the provider env vars, so they are skipped
// unless E2E_TEST_CLUSTER_PROVIDER is set (hermetic unit/CI runs are
// unaffected). Run explicitly, e.g.:
//
//	E2E_TEST_CLUSTER_PROVIDER=dvp E2E_CLUSTER_CONFIG_YAML_PATH=... \
//	go test -timeout 60m -count=1 -run TestLiveDisk -v ./pkg/e2e

const liveStepTimeout = 10 * time.Minute

// liveConnect attaches the test to the provider-managed cluster and picks the
// first worker node, skipping the test when no live cluster is configured.
func liveConnect(t *testing.T, testName string) (*e2e.Cluster, string) {
	t.Helper()

	if os.Getenv("E2E_TEST_CLUSTER_PROVIDER") == "" {
		t.Skip("E2E_TEST_CLUSTER_PROVIDER is not set; skipping live e2e test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	cl, err := e2e.Connect(ctx, e2e.WithTestName(testName))
	if errors.Is(err, e2e.ErrConnectUnsupported) {
		t.Skipf("provider does not support connecting test runs: %v", err)
	}
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close(context.Background()) })

	workers, err := kubernetes.GetWorkerNodes(ctx, cl.RESTConfig())
	if err != nil {
		t.Fatalf("list worker nodes: %v", err)
	}
	if len(workers) == 0 {
		t.Fatal("cluster has no worker nodes")
	}
	return cl, workers[0].Name
}

// TestLiveDiskLifecycle runs the full disk lifecycle against the live cluster
// via the conformance check: create, attach (the block device must appear on
// the node), detach (it must disappear), delete. A provider without disk
// support passes by consistently reporting ErrDisksUnsupported.
func TestLiveDiskLifecycle(t *testing.T) {
	cl, nodeName := liveConnect(t, "disk-lifecycle")
	t.Logf("provider %q, node %q", cl.ProviderName(), nodeName)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	if err := conformance.VerifyDiskManager(ctx, cl, nodeName); err != nil {
		t.Fatalf("disk manager conformance: %v", err)
	}
}

// TestLiveDiskAttachIsIdempotent re-attaches the same disk to the same node:
// the second call must converge on the existing attachment instead of failing.
func TestLiveDiskAttachIsIdempotent(t *testing.T) {
	cl, nodeName := liveConnect(t, "disk-attach-idempotent")

	disks := cl.Disks()
	diskName := fmt.Sprintf("e2e-disk-%s", rand.String(5))

	ctx, cancel := context.WithTimeout(t.Context(), liveStepTimeout)
	defer cancel()
	if _, err := disks.CreateDisk(ctx, e2e.DiskSpec{Name: diskName, Size: resource.MustParse("1Gi")}); err != nil {
		if errors.Is(err, e2e.ErrDisksUnsupported) {
			t.Skipf("provider %q does not support disk management: %v", cl.ProviderName(), err)
		}
		t.Fatalf("CreateDisk: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), liveStepTimeout)
		defer cancel()
		_ = disks.DetachDisk(cleanupCtx, nodeName, diskName)
		_ = disks.DeleteDisk(cleanupCtx, diskName)
	})

	attachCtx, cancel := context.WithTimeout(t.Context(), liveStepTimeout)
	defer cancel()
	if err := disks.AttachDisk(attachCtx, nodeName, diskName); err != nil {
		t.Fatalf("first AttachDisk: %v", err)
	}

	reattachCtx, cancel := context.WithTimeout(t.Context(), liveStepTimeout)
	defer cancel()
	if err := disks.AttachDisk(reattachCtx, nodeName, diskName); err != nil {
		t.Fatalf("second AttachDisk on an already attached disk: %v", err)
	}
}
