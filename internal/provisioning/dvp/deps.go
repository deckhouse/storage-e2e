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
	"log/slog"
	"time"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

type remoteExecutor interface {
	Exec(ctx context.Context, cmd string) (ssh.ExecResult, error)
}

type baseConnector interface {
	Connect(ctx context.Context) (*rest.Config, func(), error)
	VMExecutor(ctx context.Context, vmIP string) (remoteExecutor, func(), error)
}

// masterConnector opens an API connection to a freshly bootstrapped master
// (SSH kubeconfig fetch + tunnel + server rewrite). It is a narrow seam kept
// separate from baseConnector so the base cluster contract stays minimal; both
// are satisfied by the concrete *dvpConnector.
type masterConnector interface {
	connectToMaster(ctx context.Context, masterIP string) (*rest.Config, func(), error)
}

// masterResolver looks up the running master VM's internal IP on the base
// cluster. Connect needs it because a fresh run-tests process (separate from the
// bootstrap process) only has the static cluster_config.yml, which carries
// hostnames but no dynamically assigned VM IPs. Kept as its own seam so Connect
// stays unit testable without a live virtualization API.
type masterResolver interface {
	resolveMasterIP(ctx context.Context, baseKube *rest.Config, namespace, hostname string) (string, error)
}

type kubeOps interface {
	CheckReachable(ctx context.Context, kube *rest.Config) error
	WaitModuleReady(ctx context.Context, kube *rest.Config, module string, timeout time.Duration) error
	EnsureNamespace(ctx context.Context, kube *rest.Config, namespace string) error
	DeleteNamespace(ctx context.Context, kube *rest.Config, namespace string) error
}

type vmFleet interface {
	Provision(ctx context.Context, def *config.ClusterDefinition) error
	Teardown(ctx context.Context) error
}

type fleetFactory interface {
	New(ctx context.Context, kube *rest.Config, sshPublicKey string) (vmFleet, error)
}

type deps struct {
	connector    baseConnector
	masterConn   masterConnector
	resolver     masterResolver
	kube         kubeOps
	fleet        fleetFactory
	virt         virtFactory
	installReady func(ctx context.Context, kube *rest.Config, timeout time.Duration) error
}

type defaultKubeOps struct{}

func (defaultKubeOps) CheckReachable(ctx context.Context, kube *rest.Config) error {
	if _, err := kubernetes.NewClientsetWithRetry(ctx, kube); err != nil {
		return fmt.Errorf("cluster connectivity check failed: %w", err)
	}
	return nil
}

func (defaultKubeOps) WaitModuleReady(ctx context.Context, kube *rest.Config, module string, timeout time.Duration) error {
	if err := kubernetes.WaitForModuleReady(ctx, kube, module, timeout); err != nil {
		return fmt.Errorf("%s module not ready: %w", module, err)
	}
	return nil
}

func (defaultKubeOps) EnsureNamespace(ctx context.Context, kube *rest.Config, namespace string) error {
	if _, err := kubernetes.CreateNamespaceIfNotExists(ctx, kube, namespace); err != nil {
		return fmt.Errorf("ensure namespace %q: %w", namespace, err)
	}
	return nil
}

func (defaultKubeOps) DeleteNamespace(ctx context.Context, kube *rest.Config, namespace string) error {
	if err := kubernetes.DeleteNamespace(ctx, kube, namespace); err != nil {
		return fmt.Errorf("delete namespace %q: %w", namespace, err)
	}
	return nil
}

type defaultFleetFactory struct {
	dvpConf *Config
	logger  *slog.Logger
}

func (f defaultFleetFactory) New(ctx context.Context, kube *rest.Config, sshPublicKey string) (vmFleet, error) {
	virtClient, err := virtualization.NewClient(ctx, kube)
	if err != nil {
		return nil, fmt.Errorf("create virtualization client: %w", err)
	}
	return vm.NewProvisioner(vm.NewClient(virtClient), f.logger, f.provisionerConfig(sshPublicKey)), nil
}

func (f defaultFleetFactory) provisionerConfig(sshPublicKey string) vm.Config {
	return vm.Config{
		Namespace:          f.dvpConf.Namespace,
		StorageClass:       f.dvpConf.StorageClass,
		SSHPublicKey:       sshPublicKey,
		VMClassName:        f.dvpConf.VMClassName,
		DefaultVMClassName: f.dvpConf.DefaultVMClassName,
		Timeouts: vm.Timeouts{
			PollInterval:                    vmProvisionPollInterval,
			ClusterVirtualImageReadyTimeout: config.ClusterVirtualImageReadinessTimeout,
			VMClassReadyTimeout:             config.VirtualMachineClassReadinessTimeout,
			VMRunningTimeout:                config.VMsRunningTimeout,
			DeleteTimeout:                   config.ClusterCleanupTimeout,
		},
	}
}
