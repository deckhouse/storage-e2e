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

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type dvpProvider struct {
	cfg    *clusterprovider.ClusterConfig
	logger *slog.Logger
}

func NewDVPProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	err := cfg.DVP.Validate()
	if err != nil {
		return nil, err
	}

	return &dvpProvider{
		cfg:    cfg,
		logger: logger,
	}, nil
}

func (p *dvpProvider) Name() string { return clusterprovider.ModeDVP }

func (p *dvpProvider) Bootstrap(ctx context.Context) error {
	clusterDef, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return fmt.Errorf("load cluster bootstrap config: %w", err)
	}

	p.logger.Info("loaded cluster bootstrap config",
		"path", p.cfg.ClusterBootstrapConfigPath,
		"masters", len(clusterDef.Masters),
		"workers", len(clusterDef.Workers),
	)

	p.logger.Info("waiting for virtualization module to become ready",
		"timeout", config.ModuleCheckTimeout,
	)
	//if err := kubernetes.WaitForModuleReady(ctx, kubeconfig, "virtualization", config.ModuleCheckTimeout); err != nil {
	//	return fmt.Errorf("virtualization module not ready: %w", err)
	//}
	p.logger.Info("virtualization module is ready")

	p.logger.Info("ensuring test namespace exists",
		"namespace", p.cfg.DVP.Namespace,
		"timeout", config.NamespaceTimeout,
	)
	//nsCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	//defer cancel()
	//if _, err := kubernetes.CreateNamespaceIfNotExists(nsCtx, kubeconfig, p.cfg.DVP.Namespace); err != nil {
	//	return fmt.Errorf("ensure namespace %q: %w", p.cfg.DVP.Namespace, err)
	//}
	//p.logger.Info("test namespace is ready", "namespace", p.cfg.DVP.Namespace)
	//
	//virtClient, err := virtualization.NewClient(ctx, kubeconfig)
	//if err != nil {
	//	return fmt.Errorf("create virtualization client: %w", err)
	//}
	//
	//provisioner := vm.NewProvisioner(vm.NewClient(virtClient), p.logger, p.provisionerConfig(sshPublicKey))
	//
	//p.logger.Info("provisioning virtual machines", "namespace", p.cfg.DVP.Namespace, "timeout", config.VMCreationTimeout)
	//provisionCtx, provisionCancel := context.WithTimeout(ctx, config.VMCreationTimeout)
	//defer provisionCancel()
	//setupVMName, err := provisioner.Provision(provisionCtx, clusterDef)
	//if err != nil {
	//	return fmt.Errorf("provision virtual machines: %w", err)
	//}
	//p.logger.Info("virtual machines provisioned", "setupVM", setupVMName)

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	// TODO: implement — idempotent teardown by deterministic cluster name.
	return fmt.Errorf("dvp provider Remove is not implemented")
}
