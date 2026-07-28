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

package commander

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

var _ clusterprovider.Provider = (*commanderProvider)(nil)

// ConnectTestCluster attaches a test run to the Commander-managed cluster, so
// suites built on pkg/e2e (e2e.Connect) work with cluster_provider: commander and
// not only with dvp.
//
// The connection itself is the one the legacy pkg/cluster path already used
// through the Connector interface - SSH to the master via the bastion, kubeconfig
// fetched off the master, in-process API tunnel - so this reuses the connector
// rather than duplicating any of it.
//
// Disks stays nil: Commander hands out a cluster, not the infrastructure under
// it, so there is no way to attach block devices. Cluster documents Disks as
// nillable and pkg/e2e substitutes a stub that says so when a suite tries.
func (p *commanderProvider) ConnectTestCluster(ctx context.Context) (*clusterprovider.Cluster, error) {
	// Detach cancellation: the tunnel must outlive the caller's connect ctx (the
	// suite keeps the connection for its whole run); Cleanup tears it down.
	ctx = context.WithoutCancel(ctx)

	creds, err := p.conf.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve commander credentials: %w", err)
	}
	conn := newConnector(p.client, p.conf, creds, p.logger)

	// The node executor SSHes to nodes as the same user the master is reached
	// with, over the same hops.
	_, sshUser, err := conn.resolveMaster(ctx)
	if err != nil {
		return nil, err
	}

	restConfig, cleanup, err := conn.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to the commander cluster: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("build clientset for node address lookups: %w", err)
	}

	return &clusterprovider.Cluster{
		RESTConfig: restConfig,
		Nodes: &commanderNodeExecutor{
			conn:     conn,
			resolver: &internalIPResolver{clientset: clientset},
			user:     sshUser,
		},
		Disks:   nil,
		Cleanup: cleanup,
	}, nil
}
