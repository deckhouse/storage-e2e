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

package clusterprovider

import (
	"errors"

	"k8s.io/client-go/rest"
)

// ErrConnectUnsupported is returned by Provider.ConnectTestCluster when the
// provider does not support attaching test runs to its cluster (yet).
var ErrConnectUnsupported = errors.New("cluster provider does not support connecting to the test cluster")

// Cluster is a test run's connection to a provider-managed cluster: the API
// access plus the provider-specific capability strategies. It is returned by
// Provider.ConnectTestCluster, which supersedes Connector for the pkg/e2e SDK:
// where Connector yields only a rest.Config, ConnectTestCluster also carries
// the provider-specific capability strategies.
//
// Cleanup releases everything the connection holds (SSH clients, tunnels) and
// MUST be called once the run no longer needs the cluster; implementations
// must keep the connection alive until then regardless of the ctx passed to
// ConnectTestCluster.
type Cluster struct {
	// RESTConfig reaches the test cluster's API server (directly, or through
	// an in-process SSH tunnel owned by the connection).
	RESTConfig *rest.Config
	// Nodes runs commands on cluster nodes.
	Nodes NodeExecutor
	// Disks manages additional block devices on cluster nodes. Nil when the
	// provider does not support disk management.
	Disks DiskManager
	// Cleanup tears the connection down. Never nil.
	Cleanup func()
}
