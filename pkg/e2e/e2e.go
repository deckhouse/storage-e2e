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

// Package e2e is the SDK entry point for storage-e2e test suites.
//
// The CI contract stays three-phased: cmd/bootstrap-cluster provisions the
// cluster, the test binary attaches to it, cmd/remove-cluster tears it down.
// A suite attaches with Connect, which selects the provider from
// E2E_TEST_CLUSTER_PROVIDER, calls its ConnectTestCluster (API access plus the
// provider-specific capability strategies), health-checks the cluster and
// acquires the cluster lock:
//
//	cl, err := e2e.Connect(ctx)
//	defer cl.Close(context.Background())
//
//	res, err := cl.Nodes().Exec(ctx, "worker-0", "lsblk -J")
//	err = cl.Disks().AttachDisk(ctx, "worker-0", "extra-disk")
//
// Provider-neutral Kubernetes helpers from pkg/kubernetes and fixtures from
// pkg/testkit keep working as-is via cl.RESTConfig().
//
// Extending the cluster surface: a new capability is an interface in
// pkg/clusterprovider, a field on clusterprovider.Cluster the providers fill
// in, and an accessor on the e2e.Cluster facade (like Nodes()).
package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/clusterlock"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/registry"
)

// Aliases re-export the capability contracts so test code only needs the e2e
// import. The definitions live in pkg/clusterprovider, next to the Provider
// interface the concrete providers implement.
type (
	// NodeExecutor runs commands on cluster nodes.
	NodeExecutor = clusterprovider.NodeExecutor
	// ExecResult is the outcome of a node command.
	ExecResult = clusterprovider.ExecResult
	// DiskManager manages additional block devices on cluster nodes.
	DiskManager = clusterprovider.DiskManager
	// DiskSpec describes the disk to create via DiskManager.CreateDisk.
	DiskSpec = clusterprovider.DiskSpec
	// Disk describes a provider-managed additional disk.
	Disk = clusterprovider.Disk
)

// ErrConnectUnsupported is returned by Connect when the selected provider's
// ConnectTestCluster reports that it cannot attach test runs to its cluster.
var ErrConnectUnsupported = clusterprovider.ErrConnectUnsupported

// ErrDisksUnsupported is returned by DiskManager operations when the selected
// provider does not support disk management.
var ErrDisksUnsupported = clusterprovider.ErrDisksUnsupported

const (
	defaultHealthCheckTimeout = 10 * time.Minute
	healthCheckPollInterval   = 5 * time.Second
)

type connectOptions struct {
	testName           string
	acquireLock        bool
	healthCheck        bool
	healthCheckTimeout time.Duration
}

// Option customizes Connect behavior.
type Option func(*connectOptions)

// WithTestName sets the test name recorded in the cluster lock. Defaults to
// the test binary name.
func WithTestName(name string) Option {
	return func(o *connectOptions) { o.testName = name }
}

// WithoutLock skips acquiring the cluster lock. Use only for runs that are
// known to have exclusive access to the cluster (e.g. checks against a
// scratch cluster).
func WithoutLock() Option {
	return func(o *connectOptions) { o.acquireLock = false }
}

// WithoutHealthCheck skips the post-connect cluster health check.
func WithoutHealthCheck() Option {
	return func(o *connectOptions) { o.healthCheck = false }
}

// WithHealthCheckTimeout overrides the health check wait budget (default 10m).
func WithHealthCheckTimeout(d time.Duration) Option {
	return func(o *connectOptions) { o.healthCheckTimeout = d }
}

func newConnectOptions(opts []Option) connectOptions {
	o := connectOptions{
		testName:           filepath.Base(os.Args[0]),
		acquireLock:        true,
		healthCheck:        true,
		healthCheckTimeout: defaultHealthCheckTimeout,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Connect attaches the test run to the provider-managed cluster and returns a
// ready-to-use Cluster handle. It reads the provider selection from the
// environment (E2E_TEST_CLUSTER_PROVIDER), connects to the test cluster, waits
// for the cluster to be healthy and acquires the cluster lock (a
// coordination.k8s.io/v1 Lease renewed in the background; a stale lock from a
// dead run self-expires). Close the returned Cluster when the run is done.
func Connect(ctx context.Context, opts ...Option) (*Cluster, error) {
	cfg, err := clusterprovider.NewClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("read cluster provider config: %w", err)
	}

	constructor, err := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if err != nil {
		return nil, err
	}

	provider, err := constructor(logger.GetLogger(), cfg)
	if err != nil {
		return nil, fmt.Errorf("build provider %q: %w", cfg.ClusterProvider, err)
	}

	return connectWithProvider(ctx, provider, newConnectOptions(opts))
}

// connectWithProvider is the provider-injectable core of Connect.
func connectWithProvider(ctx context.Context, provider clusterprovider.Provider, o connectOptions) (*Cluster, error) {
	conn, err := provider.ConnectTestCluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect test cluster on provider %q: %w", provider.Name(), err)
	}

	cluster, err := newCluster(provider, conn)
	if err != nil {
		conn.Cleanup()
		return nil, err
	}

	if o.healthCheck {
		cs, csErr := cluster.Clientset()
		if csErr != nil {
			conn.Cleanup()
			return nil, fmt.Errorf("create clientset: %w", csErr)
		}
		if err := waitClusterHealthy(ctx, cs, o.healthCheckTimeout); err != nil {
			conn.Cleanup()
			return nil, fmt.Errorf("cluster health check: %w", err)
		}
	}

	if o.acquireLock {
		lock, err := clusterlock.AcquireLease(ctx, conn.RESTConfig, o.testName)
		if err != nil {
			conn.Cleanup()
			return nil, err
		}
		cluster.lock = lock
	}

	return cluster, nil
}
