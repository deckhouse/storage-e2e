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
)

const (
	setupNodeConnectPoll    = 10 * time.Second
	setupNodeConnectTimeout = 5 * time.Minute
)

type dvpConnector struct {
	dvpConf *Config
	creds   Credentials
	logger  *slog.Logger
}

func newConnector(dvpConf *Config, creds Credentials, logger *slog.Logger) *dvpConnector {
	return &dvpConnector{dvpConf: dvpConf, creds: creds, logger: logger}
}

func (c *dvpConnector) baseEndpoints() []ssh.Endpoint {
	base := ssh.Endpoint{
		User:       c.dvpConf.SSHUser,
		Addr:       c.dvpConf.SSHHost,
		KeyData:    c.creds.SSHKey,
		Passphrase: c.dvpConf.SSHPassphrase,
	}
	if c.dvpConf.JumpHostConfigured() {
		return []ssh.Endpoint{
			{
				User:       c.dvpConf.SSHJumpUser,
				Addr:       c.dvpConf.SSHJumpHost,
				KeyData:    c.creds.JumpKey,
				Passphrase: c.dvpConf.SSHJumpPassphrase,
			},
			base,
		}
	}
	return []ssh.Endpoint{base}
}

func (c *dvpConnector) buildSSHClient(ctx context.Context) (*ssh.Client, error) {
	hops := c.baseEndpoints()
	sshClient, err := ssh.New(ctx, ssh.Route(hops[0], hops[1:]...), ssh.WithKeepalive(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("creating ssh client: %w", err)
	}
	return sshClient, nil
}

func (c *dvpConnector) VMExecutor(ctx context.Context, vmIP string) (remoteExecutor, func(), error) {
	hops := append(c.baseEndpoints(), ssh.Endpoint{
		User:       config.VMSSHUser,
		Addr:       vmIP,
		KeyData:    c.creds.SSHKey,
		Passphrase: c.dvpConf.SSHPassphrase,
	})

	client, err := ssh.NewWithRetry(ctx, ssh.Route(hops[0], hops[1:]...),
		setupNodeConnectPoll, setupNodeConnectTimeout, ssh.WithInsecureIgnoreHostKey())
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to VM %s: %w", vmIP, err)
	}

	return client, func() {
		if closeErr := client.Close(); closeErr != nil {
			c.logger.Warn("failed to close VM ssh client", "vmIP", vmIP, "err", closeErr)
		}
	}, nil
}

func (c *dvpConnector) Connect(ctx context.Context) (*rest.Config, func(), error) {
	kubeconfigSource := "path"
	if c.dvpConf.KubeConfigContent != "" {
		kubeconfigSource = "inline"
	}
	c.logger.Info("connecting to DVP base cluster",
		"host", c.dvpConf.SSHHost,
		"jumpHost", c.dvpConf.SSHJumpHost,
		"kubeconfigSource", kubeconfigSource,
	)

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	sshClient, err := c.buildSSHClient(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("creating ssh client: %w", err)
	}
	cleanups = append(cleanups, func() {
		if closeErr := sshClient.Close(); closeErr != nil {
			c.logger.Warn("failed to close ssh client", "err", closeErr)
		}
	})

	tun, err := sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if err != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating tunnel: %w", err)
	}
	cleanups = append(cleanups, func() {
		if closeErr := tun.Close(); closeErr != nil {
			c.logger.Warn("failed to close tunnel", "err", closeErr)
		}
	})

	restConfig, err := buildRestConfig(c.creds.Kubeconfig, tun.LocalAddr())
	if err != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating rest config: %w", err)
	}

	return restConfig, runCleanups, nil
}
