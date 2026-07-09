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

package dvp

import (
	"context"
	"fmt"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

var _ clusterprovider.Provider = (*dvpProvider)(nil)

func (p *dvpProvider) ConnectTestCluster(ctx context.Context) (*clusterprovider.Cluster, error) {
	// Detach cancellation: the tunnels must outlive the caller's connect ctx
	// (the suite keeps the connection for its whole run); Cleanup tears them down.
	ctx = context.WithoutCancel(ctx)

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	clusterDef, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load cluster bootstrap config: %w", err)
	}
	if len(clusterDef.Masters) == 0 {
		return nil, fmt.Errorf("cluster definition declares no master nodes")
	}

	baseKube, baseCleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to DVP base cluster: %w", err)
	}
	cleanups = append(cleanups, baseCleanup)

	vc, err := p.deps.virt.New(ctx, baseKube)
	if err != nil {
		runCleanups()
		return nil, fmt.Errorf("create virtualization client: %w", err)
	}

	resolver := &vmIPResolver{virt: vc, namespace: p.dvpConf.Namespace}

	masterIP, err := resolver.Resolve(ctx, clusterDef.Masters[0].Hostname)
	if err != nil {
		runCleanups()
		return nil, fmt.Errorf("resolve first master VM: %w", err)
	}

	target, masterCleanup, err := p.deps.masterConn.connectToMaster(ctx, masterIP)
	if err != nil {
		runCleanups()
		return nil, fmt.Errorf("connect to master %s: %w", masterIP, err)
	}
	cleanups = append(cleanups, masterCleanup)

	return &clusterprovider.Cluster{
		RESTConfig: target,
		Nodes: &dvpNodeExecutor{
			connector: p.deps.connector,
			resolver:  resolver,
		},
		Cleanup: runCleanups,
	}, nil
}
