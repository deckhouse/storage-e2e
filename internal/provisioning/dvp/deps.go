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
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// baseConnector connects to the base cluster API and returns a cleanup.
//
// On error, implementations MUST release any partially-acquired resources
// themselves and return a nil cleanup: callers register the cleanup with defer
// only after checking the error, so a cleanup returned alongside an error would
// be dropped and leak.
type baseConnector interface {
	Connect(ctx context.Context) (*rest.Config, func(), error)
}

// kubeOps are the base-cluster Kubernetes operations the bootstrap pipeline
// needs so far. Grown per PR (YAGNI).
type kubeOps interface {
	CheckReachable(ctx context.Context, kube *rest.Config) error
	WaitModuleReady(ctx context.Context, kube *rest.Config, module string, timeout time.Duration) error
	EnsureNamespace(ctx context.Context, kube *rest.Config, namespace string) error
}

// vmFleet provisions and tears down the VM graph in the base cluster.
type vmFleet interface {
	Provision(ctx context.Context, def *config.ClusterDefinition) error
	Teardown(ctx context.Context) error
}

// fleetFactory builds a vmFleet bound to a connected base cluster. sshPublicKey
// is injected into VM cloud-init (empty for teardown-only use).
type fleetFactory interface {
	New(ctx context.Context, kube *rest.Config, sshPublicKey string) (vmFleet, error)
}

// deps holds the provider's injected collaborators.
type deps struct {
	connector baseConnector
	kube      kubeOps
	fleet     fleetFactory
}

// --- production adapters ---

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
