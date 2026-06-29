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
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

type dvpProvider struct {
	cfg     *clusterprovider.ClusterConfig
	dvpConf *Config
	creds   Credentials
	logger  *slog.Logger
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

	return newProvider(logger, cfg, dvpConf, creds), nil
}

func newProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig, dvpConf *Config, creds Credentials) *dvpProvider {
	return &dvpProvider{
		cfg:     cfg,
		dvpConf: dvpConf,
		creds:   creds,
		logger:  logger,
	}
}

func (p *dvpProvider) Name() string { return clusterprovider.ModeDVP }

func (p *dvpProvider) buildSSHClient(ctx context.Context) (*ssh.Client, error) {
	var dialer ssh.Dialer
	if p.dvpConf.JumpHostConfigured() {
		dialer = ssh.Route(ssh.Endpoint{
			User:       p.dvpConf.SSHJumpUser,
			Addr:       p.dvpConf.SSHJumpHost,
			KeyData:    p.creds.JumpKey,
			Passphrase: p.dvpConf.SSHJumpPassphrase,
		}, ssh.Endpoint{
			User:       p.dvpConf.SSHUser,
			Addr:       p.dvpConf.SSHHost,
			KeyData:    p.creds.SSHKey,
			Passphrase: p.dvpConf.SSHPassphrase,
		})
	} else {
		dialer = ssh.Route(ssh.Endpoint{
			User:       p.dvpConf.SSHUser,
			Addr:       p.dvpConf.SSHHost,
			KeyData:    p.creds.SSHKey,
			Passphrase: p.dvpConf.SSHPassphrase,
		})
	}

	sshClient, sshNewErr := ssh.New(ctx, dialer, ssh.WithKeepalive(30*time.Second))
	if sshNewErr != nil {
		return nil, fmt.Errorf("creating ssh client: %w", sshNewErr)
	}
	return sshClient, nil
}

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

	kubeconfigSource := "path"
	if p.dvpConf.KubeConfigContent != "" {
		kubeconfigSource = "inline"
	}
	p.logger.Info("connecting to DVP base cluster",
		"host", p.dvpConf.SSHHost,
		"jumpHost", p.dvpConf.SSHJumpHost,
		"kubeconfigSource", kubeconfigSource,
	)

	sshClient, sshNewErr := p.buildSSHClient(ctx)
	if sshNewErr != nil {
		return fmt.Errorf("creating ssh client: %w", sshNewErr)
	}
	defer func() {
		sshClientCloseErr := sshClient.Close()
		if sshClientCloseErr != nil {
			p.logger.Warn("failed to close ssh client", "err", sshClientCloseErr)
		}
	}()

	tun, tunErr := sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if tunErr != nil {
		return fmt.Errorf("creating tunnel: %w", tunErr)
	}
	defer func() {
		tunCloseErr := tun.Close()
		if tunCloseErr != nil {
			p.logger.Warn("failed to close tunnel", "err", tunCloseErr)
		}
	}()

	kubeconfig, buildRestConfErr := buildRestConfig(p.creds.Kubeconfig, tun.LocalAddr())
	if buildRestConfErr != nil {
		return fmt.Errorf("creating rest config: %w", buildRestConfErr)
	}

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
	return nil
}
