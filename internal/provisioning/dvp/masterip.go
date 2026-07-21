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

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
)

// firstMasterHostname returns the hostname of the first master VM in the cluster
// definition. The VM object name equals the node hostname (see vm.Provisioner),
// so the hostname is what Connect uses to look the running VM up on the base
// cluster.
func firstMasterHostname(def *config.ClusterDefinition) (string, error) {
	if def == nil {
		return "", fmt.Errorf("cluster definition is nil")
	}
	for _, m := range def.Masters {
		if m.HostType == config.HostTypeVM && m.Hostname != "" {
			return m.Hostname, nil
		}
	}
	return "", fmt.Errorf("no master VM with a hostname in cluster definition")
}

// defaultMasterResolver reads the master VM's assigned internal IP from the base
// cluster's virtualization API (VirtualMachine.status.ipAddress).
type defaultMasterResolver struct{}

func (defaultMasterResolver) resolveMasterIP(ctx context.Context, baseKube *rest.Config, namespace, hostname string) (string, error) {
	virtClient, err := virtualization.NewClient(ctx, baseKube)
	if err != nil {
		return "", fmt.Errorf("create virtualization client: %w", err)
	}
	machine, err := virtClient.VirtualMachines().Get(ctx, namespace, hostname)
	if err != nil {
		return "", fmt.Errorf("get master VM %s/%s: %w", namespace, hostname, err)
	}
	ip := machine.Status.IPAddress
	if ip == "" {
		return "", fmt.Errorf("master VM %s/%s has no assigned IP address (status.ipAddress empty)", namespace, hostname)
	}
	return ip, nil
}
