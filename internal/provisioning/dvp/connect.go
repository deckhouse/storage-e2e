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
	"github.com/deckhouse/storage-e2e/internal/kubernetes/kubeaccess"
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
		User:       c.dvpConf.VMSSHUser,
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

	// The kubeconfig may already be directly reachable (an open cluster, or a
	// run inside the same network); the SSH tunnel is only for closed clusters.
	if directConfig, err := kubeaccess.BuildRestConfigDirect(c.creds.Kubeconfig); err == nil {
		if kubeaccess.DirectReachable(ctx, directConfig) {
			c.logger.Info("base cluster kubeconfig is directly reachable, skipping SSH tunnel")
			return directConfig, func() {}, nil
		}
	}

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// The SSH client and tunnel must outlive this call — callers time-box Connect
	// with a context they cancel once it returns, so binding the persistent
	// connection to ctx would drop it immediately (later requests fail with
	// "connection refused"). Detach their lifetime from ctx; the dial/retry is
	// still bounded by the client's own timeout, and runCleanups tears them down.
	tunnelCtx := context.WithoutCancel(ctx)
	sshClient, err := c.buildSSHClient(tunnelCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("creating ssh client: %w", err)
	}
	cleanups = append(cleanups, func() {
		if closeErr := sshClient.Close(); closeErr != nil {
			c.logger.Warn("failed to close ssh client", "err", closeErr)
		}
	})

	restConfig, closeTunnel, err := kubeaccess.TunnelRestConfig(tunnelCtx, sshClient, c.creds.Kubeconfig, apiServerRemotePort)
	if err != nil {
		runCleanups()
		return nil, nil, err
	}
	cleanups = append(cleanups, func() {
		if closeErr := closeTunnel(); closeErr != nil {
			c.logger.Warn("failed to close tunnel", "err", closeErr)
		}
	})

	return restConfig, runCleanups, nil
}

// openTunnelToVM forwards a local port to remotePort on the VM at vmIP, reached
// through the base endpoints as jump hops. It is deliberately concrete (not part
// of the baseConnector seam): it is thin I/O over ssh/v2 that composes
// Route(base…, vm) with OpenTunnel. The returned close tears the tunnel down and
// closes the underlying SSH client.
func (c *dvpConnector) openTunnelToVM(ctx context.Context, vmIP string, remotePort int) (localAddr string, closeFn func() error, err error) {
	hops := append(c.baseEndpoints(), ssh.Endpoint{
		User:       c.dvpConf.VMSSHUser,
		Addr:       vmIP,
		KeyData:    c.creds.SSHKey,
		Passphrase: c.dvpConf.SSHPassphrase,
	})

	client, err := ssh.NewWithRetry(ctx, ssh.Route(hops[0], hops[1:]...),
		setupNodeConnectPoll, setupNodeConnectTimeout, ssh.WithInsecureIgnoreHostKey())
	if err != nil {
		return "", nil, fmt.Errorf("connecting to VM %s: %w", vmIP, err)
	}

	tun, err := client.OpenTunnel(ctx, remotePort)
	if err != nil {
		_ = client.Close()
		return "", nil, fmt.Errorf("opening tunnel to VM %s: %w", vmIP, err)
	}

	return tun.LocalAddr(), func() error {
		tunErr := tun.Close()
		clientErr := client.Close()
		if tunErr != nil {
			return tunErr
		}
		return clientErr
	}, nil
}

// connectToMaster fetches the admin kubeconfig off the master at masterIP over
// SSH, opens a tunnel to its API server, rewrites the kubeconfig server to point
// at the local tunnel address, and builds a rest.Config. The returned cleanup
// closes the tunnel.
func (c *dvpConnector) connectToMaster(ctx context.Context, masterIP string) (*rest.Config, func(), error) {
	exec, closeExec, err := c.VMExecutor(ctx, masterIP)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to master %s: %w", masterIP, err)
	}
	kubeconfig, fetchErr := kubeaccess.FetchKubeconfig(ctx, exec)
	closeExec()
	if fetchErr != nil {
		return nil, nil, fmt.Errorf("fetching kubeconfig from master %s: %w", masterIP, fetchErr)
	}

	// The API tunnel must outlive this call: callers time-box Connect with a
	// context they cancel as soon as it returns (e.g.
	// cluster.CreateOrConnectToTestCluster does `defer cancel()`). Binding the
	// persistent tunnel to that ctx would tear it down immediately after connect,
	// so every later request to the local tunnel address fails with
	// "connection refused". Detach the tunnel's lifetime from ctx (ctx still
	// scopes the dial/retry); the returned cleanup closes it.
	localAddr, closeTunnel, err := c.openTunnelToVM(context.WithoutCancel(ctx), masterIP, apiServerRemotePort)
	if err != nil {
		return nil, nil, err
	}

	rewritten, err := kubeaccess.RewriteServer(kubeconfig, localAddr)
	if err != nil {
		if closeErr := closeTunnel(); closeErr != nil {
			c.logger.Warn("failed to close master tunnel after rewrite error", "masterIP", masterIP, "err", closeErr)
		}
		return nil, nil, fmt.Errorf("rewriting master kubeconfig: %w", err)
	}

	restConfig, err := kubeaccess.BuildRestConfigDirect(rewritten)
	if err != nil {
		if closeErr := closeTunnel(); closeErr != nil {
			c.logger.Warn("failed to close master tunnel after rest config error", "masterIP", masterIP, "err", closeErr)
		}
		return nil, nil, fmt.Errorf("building master rest config: %w", err)
	}

	cleanup := func() {
		if closeErr := closeTunnel(); closeErr != nil {
			c.logger.Warn("failed to close master tunnel", "masterIP", masterIP, "err", closeErr)
		}
	}
	return restConfig, cleanup, nil
}
