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

	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

const getKubeconfigCmd = "sudo -n /bin/cat /etc/kubernetes/super-admin.conf 2>/dev/null " +
	"|| sudo -n /bin/cat /etc/kubernetes/admin.conf"

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
	kubeconfig, fetchErr := fetchKubeconfig(ctx, exec)
	closeExec()
	if fetchErr != nil {
		return nil, nil, fmt.Errorf("fetching kubeconfig from master %s: %w", masterIP, fetchErr)
	}

	localAddr, closeTunnel, err := c.openTunnelToVM(ctx, masterIP, apiServerRemotePort)
	if err != nil {
		return nil, nil, err
	}

	rewritten, err := rewriteKubeconfigServer(kubeconfig, localAddr)
	if err != nil {
		if closeErr := closeTunnel(); closeErr != nil {
			c.logger.Warn("failed to close master tunnel after rewrite error", "masterIP", masterIP, "err", closeErr)
		}
		return nil, nil, fmt.Errorf("rewriting master kubeconfig: %w", err)
	}

	restConfig, err := buildRestConfigFromKubeconfig(rewritten)
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

// fetchKubeconfig runs getKubeconfigCmd over the executor and returns the raw
// kubeconfig bytes. A non-zero exit (neither super-admin.conf nor admin.conf
// readable) is surfaced with the captured stderr for diagnosis.
func fetchKubeconfig(ctx context.Context, exec remoteExecutor) ([]byte, error) {
	res, err := exec.Exec(ctx, getKubeconfigCmd)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig over ssh: %w (stderr: %s)", err, string(res.Stderr))
	}
	if len(res.Stdout) == 0 {
		return nil, fmt.Errorf("empty kubeconfig from master (stderr: %s)", string(res.Stderr))
	}
	return res.Stdout, nil
}
