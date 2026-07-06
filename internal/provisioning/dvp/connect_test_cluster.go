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
	"errors"
	"fmt"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

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

	virtClient, err := virtualization.NewClient(ctx, baseKube)
	if err != nil {
		runCleanups()
		return nil, fmt.Errorf("create virtualization client: %w", err)
	}

	resolver := &vmIPResolver{virtClient: virtClient, namespace: p.dvpConf.Namespace}

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
		Disks: &dvpDiskManager{
			virtClient:          virtClient,
			namespace:           p.dvpConf.Namespace,
			defaultStorageClass: p.dvpConf.StorageClass,
			logger:              p.logger,
		},
		Cleanup: runCleanups,
	}, nil
}

// vmIPResolver maps a cluster node name to its VM IP on the base cluster
// (node names equal VM names — both come from ClusterDefinition hostnames).
type vmIPResolver struct {
	virtClient *virtualization.Client
	namespace  string
}

func (r *vmIPResolver) Resolve(ctx context.Context, nodeName string) (string, error) {
	machine, err := r.virtClient.VirtualMachines().Get(ctx, r.namespace, nodeName)
	if err != nil {
		return "", fmt.Errorf("get VM %s/%s: %w", r.namespace, nodeName, err)
	}
	if machine.Status.IPAddress == "" {
		return "", fmt.Errorf("VM %s/%s has no IP address in status", r.namespace, nodeName)
	}
	return machine.Status.IPAddress, nil
}

type dvpNodeExecutor struct {
	connector baseConnector
	resolver  *vmIPResolver
}

func (e *dvpNodeExecutor) Exec(ctx context.Context, nodeName, command string) (clusterprovider.ExecResult, error) {
	ip, err := e.resolver.Resolve(ctx, nodeName)
	if err != nil {
		return clusterprovider.ExecResult{}, err
	}

	exec, closeExec, err := e.connector.VMExecutor(ctx, ip)
	if err != nil {
		return clusterprovider.ExecResult{}, fmt.Errorf("connect to node %s (%s): %w", nodeName, ip, err)
	}
	defer closeExec()

	res, err := exec.Exec(ctx, command)
	out := clusterprovider.ExecResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
	}
	if _, ok := errors.AsType[*cryptossh.ExitError](err); ok {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("exec on node %s (%s): %w", nodeName, ip, err)
	}
	return out, nil
}

var (
	_ clusterprovider.NodeExecutor = (*dvpNodeExecutor)(nil)
	_ clusterprovider.Provider     = (*dvpProvider)(nil)
)
