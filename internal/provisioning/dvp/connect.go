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

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

// dvpConnector opens an SSH tunnel to the base cluster API server and builds a
// rest.Config pointed at the tunnel. It is the production baseConnector.
type dvpConnector struct {
	dvpConf *Config
	creds   Credentials
	logger  *slog.Logger
}

func newConnector(dvpConf *Config, creds Credentials, logger *slog.Logger) *dvpConnector {
	return &dvpConnector{dvpConf: dvpConf, creds: creds, logger: logger}
}

func (c *dvpConnector) buildSSHClient(ctx context.Context) (*ssh.Client, error) {
	var dialer ssh.Dialer
	if c.dvpConf.JumpHostConfigured() {
		dialer = ssh.Route(ssh.Endpoint{
			User:       c.dvpConf.SSHJumpUser,
			Addr:       c.dvpConf.SSHJumpHost,
			KeyData:    c.creds.JumpKey,
			Passphrase: c.dvpConf.SSHJumpPassphrase,
		}, ssh.Endpoint{
			User:       c.dvpConf.SSHUser,
			Addr:       c.dvpConf.SSHHost,
			KeyData:    c.creds.SSHKey,
			Passphrase: c.dvpConf.SSHPassphrase,
		})
	} else {
		dialer = ssh.Route(ssh.Endpoint{
			User:       c.dvpConf.SSHUser,
			Addr:       c.dvpConf.SSHHost,
			KeyData:    c.creds.SSHKey,
			Passphrase: c.dvpConf.SSHPassphrase,
		})
	}

	sshClient, err := ssh.New(ctx, dialer, ssh.WithKeepalive(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("creating ssh client: %w", err)
	}
	return sshClient, nil
}

// Connect returns a rest.Config for the base cluster and a cleanup that tears
// down the tunnel and SSH client (run in reverse order).
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
		if err := sshClient.Close(); err != nil {
			c.logger.Warn("failed to close ssh client", "err", err)
		}
	})

	tun, err := sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if err != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating tunnel: %w", err)
	}
	cleanups = append(cleanups, func() {
		if err := tun.Close(); err != nil {
			c.logger.Warn("failed to close tunnel", "err", err)
		}
	})

	restConfig, err := buildRestConfig(c.creds.Kubeconfig, tun.LocalAddr())
	if err != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating rest config: %w", err)
	}

	return restConfig, runCleanups, nil
}
