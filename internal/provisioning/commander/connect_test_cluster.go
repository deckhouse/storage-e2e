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

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

var _ clusterprovider.Provider = (*commanderProvider)(nil)

// ConnectTestCluster attaches a test run to the Commander-managed cluster, so
// suites built on pkg/e2e (e2e.Connect) work with cluster_provider: commander and
// not only with dvp.
//
// The connection itself is the one the legacy pkg/cluster path already used
// through the Connector interface - SSH to the master via the bastion, kubeconfig
// fetched off the master, in-process API tunnel - so this delegates to Connect
// rather than duplicating it.
//
// Nodes and Disks are nil: Commander hands out a cluster, not the infrastructure
// under it, so there is no node-exec transport and no way to attach block
// devices. Suites that need either must run on a provider that offers them (dvp);
// suites that only talk to the Kubernetes API - the common case for a storage
// module whose backend is external - are fully served here.
func (p *commanderProvider) ConnectTestCluster(ctx context.Context) (*clusterprovider.Cluster, error) {
	restConfig, cleanup, err := p.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to the commander cluster: %w", err)
	}

	return &clusterprovider.Cluster{
		RESTConfig: restConfig,
		Nodes:      nil,
		Disks:      nil,
		Cleanup:    cleanup,
	}, nil
}
