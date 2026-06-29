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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

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
	logger  *slog.Logger
}

func NewDVPProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	dvpConf := &Config{}
	if err := env.Parse(dvpConf); err != nil {
		return nil, err
	}

	return &dvpProvider{
		cfg:     cfg,
		dvpConf: dvpConf,
		logger:  logger,
	}, nil
}

func (p *dvpProvider) Name() string { return clusterprovider.ModeDVP }

func (p *dvpProvider) buildSSHClient(ctx context.Context) (*ssh.Client, error) {
	var dialer ssh.Dialer
	if p.dvpConf.HasJumpHost() {
		dialer = ssh.Route(ssh.Endpoint{
			User:       p.dvpConf.SSHJumpUser,
			Addr:       p.dvpConf.SSHJumpHost,
			KeyPath:    p.dvpConf.SSHJumpKeyPath,
			Passphrase: p.dvpConf.SSHJumpPassphrase,
		}, ssh.Endpoint{
			User:       p.dvpConf.SSHUser,
			Addr:       p.dvpConf.SSHHost,
			KeyPath:    p.dvpConf.SSHKeyPath,
			Passphrase: p.dvpConf.SSHPassphrase,
		})
	} else {
		dialer = ssh.Route(ssh.Endpoint{
			User:       p.dvpConf.SSHUser,
			Addr:       p.dvpConf.SSHHost,
			KeyPath:    p.dvpConf.SSHKeyPath,
			Passphrase: p.dvpConf.SSHPassphrase,
		})
	}

	sshClient, sshNewErr := ssh.New(ctx, dialer, ssh.WithKeepalive(30*time.Second))
	if sshNewErr != nil {
		return nil, fmt.Errorf("creating ssh client: %w", sshNewErr)
	}
	return sshClient, nil
}

func (p *dvpProvider) buildRestConfig(tun *ssh.Tunnel) (*rest.Config, error) {
	rawKubeconfig, readErr := readKubeconfig(p.dvpConf.KubeConfigPath)
	if readErr != nil {
		return nil, fmt.Errorf("reading kubeconfig: %w", readErr)
	}

	apiCfg, err := clientcmd.Load(rawKubeconfig)
	overrides := &clientcmd.ConfigOverrides{
		ClusterInfo: clientcmdapi.Cluster{
			Server: tun.LocalAddr(),
		},
		Timeout: (2 * time.Minute).String(),
	}

	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	restConfig, clientConfigErr := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if clientConfigErr != nil {
		return nil, fmt.Errorf("creating client config: %w", clientConfigErr)
	}

	configureTunnelTimeouts(restConfig)
	return restConfig, nil
}

func (p *dvpProvider) connect(ctx context.Context) (*rest.Config, func(), error) {
	p.logger.Info("connecting to DVP base cluster",
		"host", p.dvpConf.SSHHost,
		"jumpHost", p.dvpConf.SSHJumpHost,
		"kubeconfigSource", p.dvpConf.KubeConfigPath,
	)

	sshClient, sshNewErr := p.buildSSHClient(ctx)
	if sshNewErr != nil {
		return nil, nil, fmt.Errorf("creating ssh client: %w", sshNewErr)
	}

	tun, tunErr := sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if tunErr != nil {
		if closeErr := sshClient.Close(); closeErr != nil {
			p.logger.Warn("failed to close ssh client", "err", closeErr)
		}
		return nil, nil, fmt.Errorf("creating tunnel: %w", tunErr)
	}

	kubeconfig, buildRestConfErr := p.buildRestConfig(tun)
	if buildRestConfErr != nil {
		if closeErr := tun.Close(); closeErr != nil {
			p.logger.Warn("failed to close tunnel", "err", closeErr)
		}
		if closeErr := sshClient.Close(); closeErr != nil {
			p.logger.Warn("failed to close ssh client", "err", closeErr)
		}
		return nil, nil, fmt.Errorf("creating rest config: %w", buildRestConfErr)
	}

	cleanup := func() {
		if tunCloseErr := tun.Close(); tunCloseErr != nil {
			p.logger.Warn("failed to close tunnel", "err", tunCloseErr)
		}
		if sshClientCloseErr := sshClient.Close(); sshClientCloseErr != nil {
			p.logger.Warn("failed to close ssh client", "err", sshClientCloseErr)
		}
	}
	return kubeconfig, cleanup, nil
}

func (p *dvpProvider) provisionerConfig(sshPublicKey, setupSuffix string) vm.Config {
	return vm.Config{
		Namespace:                       p.dvpConf.Namespace,
		StorageClass:                    p.dvpConf.StorageClass,
		SSHPublicKey:                    sshPublicKey,
		VMClassName:                     p.dvpConf.VMClassName,
		DefaultVMClassName:              p.dvpConf.DefaultVMClassName,
		RunLabel:                        p.dvpConf.Namespace,
		SetupVMNameSuffix:               setupSuffix,
		PollInterval:                    vmProvisionPollInterval,
		ClusterVirtualImageReadyTimeout: config.ClusterVirtualImageReadinessTimeout,
		VMClassReadyTimeout:             config.VirtualMachineClassReadinessTimeout,
		VMRunningTimeout:                config.VMsRunningTimeout,
		DeleteTimeout:                   config.ClusterCleanupTimeout,
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

	sshPublicKey, pubKeyErr := readSSHPublicKey(p.dvpConf.SSHKeyPath)
	if pubKeyErr != nil {
		return fmt.Errorf("read ssh public key: %w", pubKeyErr)
	}

	setupSuffix, suffixErr := randomSuffix()
	if suffixErr != nil {
		return fmt.Errorf("generate setup VM name suffix: %w", suffixErr)
	}

	kubeconfig, cleanup, connErr := p.connect(ctx)
	if connErr != nil {
		return connErr
	}
	defer cleanup()

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

	provisioner := vm.NewProvisioner(vm.NewClient(virtClient), p.logger, p.provisionerConfig(sshPublicKey, setupSuffix))

	p.logger.Info("provisioning virtual machines",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.VMCreationTimeout,
	)
	provisionCtx, provisionCancel := context.WithTimeout(ctx, config.VMCreationTimeout)
	defer provisionCancel()
	setupVMName, provisionErr := provisioner.Provision(provisionCtx, clusterDef)
	if provisionErr != nil {
		return fmt.Errorf("provision virtual machines: %w", provisionErr)
	}
	p.logger.Info("virtual machines provisioned", "setupVM", setupVMName)

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

	provisioner := vm.NewProvisioner(vm.NewClient(virtClient), p.logger, p.provisionerConfig("", ""))

	p.logger.Info("tearing down virtual machines",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.ClusterCleanupTimeout,
	)
	teardownCtx, teardownCancel := context.WithTimeout(ctx, config.ClusterCleanupTimeout)
	defer teardownCancel()
	if err := provisioner.Teardown(teardownCtx); err != nil {
		return fmt.Errorf("teardown virtual machines: %w", err)
	}
	p.logger.Info("virtual machines torn down", "namespace", p.dvpConf.Namespace)

	return nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
