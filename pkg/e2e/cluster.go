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
// the API access (rest.Config plus lazily-built cached clients) with the
// provider-specific capability strategies (node command execution). Obtain it
// with Connect and release it with Close.
type Cluster struct {
	provider clusterprovider.Provider
	conn     *clusterprovider.Cluster

	clientsetOnce sync.Once
	clientset     kubernetes.Interface
	clientsetErr  error

	dynamicOnce sync.Once
	dynamic     dynamic.Interface
	dynamicErr  error

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
	return &Cluster{provider: provider, conn: conn}, nil
}

// ProviderName reports which provider manages the cluster ("dvp", "commander").
func (c *Cluster) ProviderName() string { return c.provider.Name() }

// RESTConfig returns the rest.Config pointed at the test cluster's API server.
// Pass it to the provider-neutral helpers in pkg/kubernetes and pkg/testkit.
func (c *Cluster) RESTConfig() *rest.Config { return c.conn.RESTConfig }

// Clientset returns a cached typed Kubernetes client for the test cluster.
func (c *Cluster) Clientset() (kubernetes.Interface, error) {
	c.clientsetOnce.Do(func() {
		c.clientset, c.clientsetErr = kubernetes.NewForConfig(c.conn.RESTConfig)
	})
	return c.clientset, c.clientsetErr
}

// Dynamic returns a cached dynamic Kubernetes client for the test cluster.
func (c *Cluster) Dynamic() (dynamic.Interface, error) {
	c.dynamicOnce.Do(func() {
		c.dynamic, c.dynamicErr = dynamic.NewForConfig(c.conn.RESTConfig)
	})
	return c.dynamic, c.dynamicErr
}

// Nodes returns the provider's node command executor.
func (c *Cluster) Nodes() NodeExecutor { return c.conn.Nodes }

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
