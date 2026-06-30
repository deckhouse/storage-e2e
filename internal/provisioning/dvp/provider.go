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

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp/vm"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const vmProvisionPollInterval = 5 * time.Second

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

func (p *dvpProvider) connect(ctx context.Context) (*rest.Config, func(), error) {
	kubeconfigSource := "path"
	if p.dvpConf.KubeConfigContent != "" {
		kubeconfigSource = "inline"
	}
	p.logger.Info("connecting to DVP base cluster",
		"host", p.dvpConf.SSHHost,
		"jumpHost", p.dvpConf.SSHJumpHost,
		"kubeconfigSource", kubeconfigSource,
	)

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	sshClient, sshNewErr := p.buildSSHClient(ctx)
	if sshNewErr != nil {
		return nil, nil, fmt.Errorf("creating ssh client: %w", sshNewErr)
	}
	cleanups = append(cleanups, func() {
		if err := sshClient.Close(); err != nil {
			p.logger.Warn("failed to close ssh client", "err", err)
		}
	})

	tun, tunErr := sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if tunErr != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating tunnel: %w", tunErr)
	}
	cleanups = append(cleanups, func() {
		if err := tun.Close(); err != nil {
			p.logger.Warn("failed to close tunnel", "err", err)
		}
	})

	restConfig, buildRestConfErr := buildRestConfig(p.creds.Kubeconfig, tun.LocalAddr())
	if buildRestConfErr != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating rest config: %w", buildRestConfErr)
	}

	return restConfig, runCleanups, nil
}

func (p *dvpProvider) provisionerConfig(sshPublicKey string) vm.Config {
	return vm.Config{
		Namespace:          p.dvpConf.Namespace,
		StorageClass:       p.dvpConf.StorageClass,
		SSHPublicKey:       sshPublicKey,
		VMClassName:        p.dvpConf.VMClassName,
		DefaultVMClassName: p.dvpConf.DefaultVMClassName,
		Timeouts: vm.Timeouts{
			PollInterval:                    vmProvisionPollInterval,
			ClusterVirtualImageReadyTimeout: config.ClusterVirtualImageReadinessTimeout,
			VMClassReadyTimeout:             config.VirtualMachineClassReadinessTimeout,
			VMRunningTimeout:                config.VMsRunningTimeout,
			DeleteTimeout:                   config.ClusterCleanupTimeout,
		},
	}
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

	sshPublicKey, pubKeyErr := publicKeyFromPrivateKey(p.creds.SSHKey, p.dvpConf.SSHPassphrase)
	if pubKeyErr != nil {
		return fmt.Errorf("derive ssh public key: %w", pubKeyErr)
	}

	kubeconfig, cleanup, connErr := p.connect(ctx)
	if connErr != nil {
		return connErr
	}
	defer cleanup()

	p.logger.Info("verifying connectivity to DVP base cluster API server")
	if _, err := kubernetes.NewClientsetWithRetry(ctx, kubeconfig); err != nil {
		return fmt.Errorf("cluster connectivity check failed: %w", err)
	}
	p.logger.Info("DVP base cluster API server is reachable")

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

	virtClient, virtClientErr := virtualization.NewClient(ctx, kubeconfig)
	if virtClientErr != nil {
		return fmt.Errorf("create virtualization client: %w", virtClientErr)
	}

	provisioner := vm.NewProvisioner(vm.NewClient(virtClient), p.logger, p.provisionerConfig(sshPublicKey))

	p.logger.Info("provisioning virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	if provisionErr := provisioner.Provision(ctx, clusterDef); provisionErr != nil {
		return fmt.Errorf("provision virtual machines: %w", provisionErr)
	}
	p.logger.Info("virtual machines provisioned", "namespace", p.dvpConf.Namespace)

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	kubeconfig, cleanup, connErr := p.connect(ctx)
	if connErr != nil {
		return connErr
	}
	defer cleanup()

	virtClient, virtClientErr := virtualization.NewClient(ctx, kubeconfig)
	if virtClientErr != nil {
		return fmt.Errorf("create virtualization client: %w", virtClientErr)
	}

	provisioner := vm.NewProvisioner(vm.NewClient(virtClient), p.logger, p.provisionerConfig(""))

	p.logger.Info("tearing down virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	// Per-resource DeleteTimeout (from vm.Timeouts) governs each deletion wait,
	// so we pass the caller context through without an umbrella timeout.
	if err := provisioner.Teardown(ctx); err != nil {
		return fmt.Errorf("teardown virtual machines: %w", err)
	}
	p.logger.Info("virtual machines torn down", "namespace", p.dvpConf.Namespace)

	return nil
}
