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

	"github.com/caarlos0/env/v11"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

type dvpProvider struct {
	cfg     *clusterprovider.ClusterConfig
	dvpConf *Config
	logger  *slog.Logger
}

func NewDVPProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	dvpConf := &Config{}
	if err := env.Parse(dvpConf); err != nil {
		return nil, err
	}
	err := dvpConf.SetPassphrase()
	if err != nil {
		return nil, err
	}

	return &dvpProvider{
		cfg:     cfg,
		dvpConf: dvpConf,
		logger:  logger,
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

	p.logger.Info("connecting to DVP base cluster",
		"host", p.dvpConf.SSHHost,
		"jumpHost", p.dvpConf.SSHJumpHost,
		"kubeconfigSource", p.dvpConf.KubeConfigPath,
	)
	conn, err := openTunnel(ctx, p.dvpConf.baseEndpoint())
	if err != nil {
		return fmt.Errorf("open tunnel to DVP base cluster: %w", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			p.logger.Warn("close DVP base cluster connection", "err", cerr)
		}
	}()

	kubeconfig, kubeconfigPath, err := loadKubeconfigViaTunnel(
		conn.tunnel.LocalPort, config.E2ETempDir, p.dvpConf.SSHHost, p.dvpConf.KubeConfigPath,
	)
	if err != nil {
		return fmt.Errorf("build kubeconfig for DVP base cluster: %w", err)
	}
	p.logger.Info("connected to DVP base cluster",
		"kubeconfig", kubeconfigPath,
		"apiServer", kubeconfig.Host,
	)

	p.logger.Info("waiting for virtualization module to become ready",
		"timeout", config.ModuleCheckTimeout,
	)
	if err := kubernetes.WaitForModuleReady(ctx, kubeconfig, "virtualization", config.ModuleCheckTimeout); err != nil {
		return fmt.Errorf("virtualization module not ready: %w", err)
	}
	p.logger.Info("virtualization module is ready")

	p.logger.Info("ensuring test namespace exists",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.NamespaceTimeout,
	)
	nsCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	defer cancel()
	if _, err := kubernetes.CreateNamespaceIfNotExists(nsCtx, kubeconfig, p.dvpConf.Namespace); err != nil {
		return fmt.Errorf("ensure namespace %q: %w", p.dvpConf.Namespace, err)
	}
	p.logger.Info("test namespace is ready", "namespace", p.dvpConf.Namespace)

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	// TODO: implement — idempotent teardown by deterministic cluster name.
	panic("not implemented")
}
