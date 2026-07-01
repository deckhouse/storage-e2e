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
	"os"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

const vmProvisionPollInterval = 5 * time.Second

type dvpProvider struct {
	cfg     *clusterprovider.ClusterConfig
	dvpConf *Config
	creds   Credentials
	logger  *slog.Logger
	deps    deps
}

func NewDVPProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	dvpConf, err := LoadConfig(env.ToMap(os.Environ()))
	if err != nil {
		return nil, err
	}

	creds, err := dvpConf.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolving credentials: %w", err)
	}

	d := deps{
		connector: newConnector(dvpConf, creds, logger),
		kube:      defaultKubeOps{},
		fleet:     defaultFleetFactory{dvpConf: dvpConf, logger: logger},
	}
	return newProvider(logger, cfg, dvpConf, creds, d), nil
}

func newProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig, dvpConf *Config, creds Credentials, d deps) *dvpProvider {
	return &dvpProvider{
		cfg:     cfg,
		dvpConf: dvpConf,
		creds:   creds,
		logger:  logger,
		deps:    d,
	}
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

	sshPublicKey, err := publicKeyFromPrivateKey(p.creds.SSHKey, p.dvpConf.SSHPassphrase)
	if err != nil {
		return fmt.Errorf("derive ssh public key: %w", err)
	}

	kube, cleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	p.logger.Info("verifying connectivity to DVP base cluster API server")
	if reachErr := p.deps.kube.CheckReachable(ctx, kube); reachErr != nil {
		return reachErr
	}
	p.logger.Info("DVP base cluster API server is reachable")

	p.logger.Info("waiting for virtualization module to become ready",
		"timeout", config.ModuleCheckTimeout,
	)
	if moduleErr := p.deps.kube.WaitModuleReady(ctx, kube, "virtualization", config.ModuleCheckTimeout); moduleErr != nil {
		return moduleErr
	}
	p.logger.Info("virtualization module is ready")

	p.logger.Info("ensuring test namespace exists",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.NamespaceTimeout,
	)
	nsCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	defer cancel()
	if nsErr := p.deps.kube.EnsureNamespace(nsCtx, kube, p.dvpConf.Namespace); nsErr != nil {
		return nsErr
	}
	p.logger.Info("test namespace is ready", "namespace", p.dvpConf.Namespace)

	fleet, err := p.deps.fleet.New(ctx, kube, sshPublicKey)
	if err != nil {
		return err
	}

	p.logger.Info("provisioning virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	if err := fleet.Provision(ctx, clusterDef); err != nil {
		return fmt.Errorf("provision virtual machines: %w", err)
	}
	p.logger.Info("virtual machines provisioned", "namespace", p.dvpConf.Namespace)

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	kube, cleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	fleet, err := p.deps.fleet.New(ctx, kube, "")
	if err != nil {
		return err
	}

	p.logger.Info("tearing down virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	if err := fleet.Teardown(ctx); err != nil {
		return fmt.Errorf("teardown virtual machines: %w", err)
	}
	p.logger.Info("virtual machines torn down", "namespace", p.dvpConf.Namespace)

	return nil
}
