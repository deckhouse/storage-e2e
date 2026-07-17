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

package e2e

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/clusterlock"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

// Cluster is the test run's handle to a provider-managed cluster. It bundles
// the API access (rest.Config plus ready-built cached clients) with the
// provider-specific capability strategies (node command execution). Obtain it
// with Connect and release it with Close.
//
// Invariant: a Cluster returned by Connect always carries valid, ready-to-use
// clients — client construction failures fail Connect itself. Keep any future
// client that is expensive to build (network calls, API discovery) out of this
// eager set and expose it through a lazy, error-returning accessor instead.
type Cluster struct {
	provider clusterprovider.Provider
	conn     *clusterprovider.Cluster

	clientset kubernetes.Interface
	dynamic   dynamic.Interface

	lock      *clusterlock.LeaseLock
	closeOnce sync.Once
	closeErr  error
}

func newCluster(provider clusterprovider.Provider, conn *clusterprovider.Cluster) (*Cluster, error) {
	if conn.RESTConfig == nil {
		return nil, fmt.Errorf("provider %q returned a cluster without a rest.Config", provider.Name())
	}
	if conn.Nodes == nil {
		return nil, fmt.Errorf("provider %q returned a cluster without a NodeExecutor", provider.Name())
	}
	if conn.Cleanup == nil {
		conn.Cleanup = func() {}
	}
	if conn.Disks == nil {
		conn.Disks = unsupportedDiskManager{provider: provider.Name()}
	}
	clientset, err := kubernetes.NewForConfig(conn.RESTConfig)
	if err != nil {
		return nil, fmt.Errorf("provider %q: build clientset: %w", provider.Name(), err)
	}
	dynamicClient, err := dynamic.NewForConfig(conn.RESTConfig)
	if err != nil {
		return nil, fmt.Errorf("provider %q: build dynamic client: %w", provider.Name(), err)
	}
	return &Cluster{
		provider:  provider,
		conn:      conn,
		clientset: clientset,
		dynamic:   dynamicClient,
	}, nil
}

// ProviderName reports which provider manages the cluster ("dvp", "commander").
func (c *Cluster) ProviderName() string { return c.provider.Name() }

// RESTConfig returns the rest.Config pointed at the test cluster's API server.
// Pass it to the provider-neutral helpers in pkg/kubernetes and pkg/testkit.
func (c *Cluster) RESTConfig() *rest.Config { return c.conn.RESTConfig }

// Clientset returns the cached typed Kubernetes client for the test cluster.
// The client is built eagerly by Connect, which fails when construction fails.
func (c *Cluster) Clientset() kubernetes.Interface { return c.clientset }

// Dynamic returns the cached dynamic Kubernetes client for the test cluster.
// The client is built eagerly by Connect, which fails when construction fails.
func (c *Cluster) Dynamic() dynamic.Interface { return c.dynamic }

// Nodes returns the provider's node command executor.
func (c *Cluster) Nodes() NodeExecutor { return c.conn.Nodes }

// Disks returns the provider's disk manager. When the provider does not
// support disk management (e.g. commander, for now) every operation on the
// returned manager fails with ErrDisksUnsupported.
func (c *Cluster) Disks() DiskManager { return c.conn.Disks }

// unsupportedDiskManager is substituted when a provider leaves Disks nil.
type unsupportedDiskManager struct {
	provider string
}

var _ clusterprovider.DiskManager = unsupportedDiskManager{}

func (u unsupportedDiskManager) err() error {
	return fmt.Errorf("provider %q: %w", u.provider, clusterprovider.ErrDisksUnsupported)
}

func (u unsupportedDiskManager) CreateDisk(context.Context, DiskSpec) (*Disk, error) {
	return nil, u.err()
}

func (u unsupportedDiskManager) DeleteDisk(context.Context, string) error { return u.err() }

func (u unsupportedDiskManager) AttachDisk(context.Context, string, string) error { return u.err() }

func (u unsupportedDiskManager) DetachDisk(context.Context, string, string) error { return u.err() }

// Close releases everything the run holds on the cluster: the cluster lock
// (when acquired by Connect) and the provider connection (SSH clients,
// tunnels). It is idempotent; subsequent calls return the first result.
func (c *Cluster) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		if c.lock != nil {
			c.closeErr = c.lock.Release(ctx)
		}
		c.conn.Cleanup()
	})
	return c.closeErr
}
