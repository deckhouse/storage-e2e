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

// Package clusterprovider defines the provider abstraction used to bootstrap
// and tear down test clusters, along with the provider mode and env-based
// configuration shared by concrete provider implementations.
package clusterprovider

import (
	"context"

	"k8s.io/client-go/rest"
)

// Provider provisions and removes a test cluster for a specific backend
// (for example DVP) and attaches test runs to it. Bootstrap and Remove are
// expected to be idempotent.
//
// ConnectTestCluster attaches a test run to the bootstrapped cluster,
// returning the API access plus the provider-specific capability strategies
// (see Cluster). Providers that cannot connect test runs yet return
// ErrConnectUnsupported.
type Provider interface {
	Name() string
	Bootstrap(ctx context.Context) error
	Remove(ctx context.Context) error
	ConnectTestCluster(ctx context.Context) (*Cluster, error)
}

// Connector is an optional Provider capability: attaching a test run to the
// provider-managed cluster, returning a rest.Config plus a cleanup that
// releases the connection (e.g. an SSH tunnel + client). Providers whose
// Bootstrap yields a directly-connectable cluster (e.g. commander) implement it,
// and the suite uses it in place of the legacy TEST_CLUSTER_CREATE_MODE connect
// path; providers that do not (yet) implement it keep using that legacy path.
// The returned cleanup MUST be called once the run no longer needs the
// connection; implementations must keep the connection alive until then
// regardless of the ctx passed to Connect.
type Connector interface {
	Connect(ctx context.Context) (*rest.Config, func(), error)
}
