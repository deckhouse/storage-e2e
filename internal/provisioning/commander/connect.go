/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commander

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	commanderapi "github.com/deckhouse/storage-e2e/internal/kubernetes/commander"
)

const (
	// The master was already waited to Ready by Bootstrap; a short retry only
	// absorbs the SSH daemon settling after that.
	connectRetryEvery = 5 * time.Second
	connectTimeout    = 2 * time.Minute
)

// connector reaches the Commander cluster's master over SSH (optionally via a
// jump host), fetches its kubeconfig, and opens an API tunnel — mirroring the
// DVP connector, except the master host is resolved from the Commander
// connection info and the kubeconfig is read off the master rather than
// supplied as input.
type connector struct {
	client *commanderapi.Client
	conf   *Config
	creds  Credentials
	logger *slog.Logger
}

func newConnector(client *commanderapi.Client, conf *Config, creds Credentials, logger *slog.Logger) *connector {
	return &connector{client: client, conf: conf, creds: creds, logger: logger}
}

// resolveMaster returns the master SSH host and user for the cluster.
func (c *connector) resolveMaster(ctx context.Context) (host, user string, err error) {
	conn, err := c.client.GetClusterConnectionInfo(ctx, c.conf.ClusterName)
	if err != nil {
		return "", "", fmt.Errorf("get connection info for cluster %q: %w", c.conf.ClusterName, err)
	}
	if conn.SSHHost == "" {
		return "", "", fmt.Errorf("Commander returned no SSH host for cluster %q (connection_hosts.masters empty)", c.conf.ClusterName)
	}
	user = c.conf.SSHUser
	if user == "" {
		user = conn.SSHUser
	}
	if user == "" {
		return "", "", fmt.Errorf("no SSH user for the master of cluster %q (set E2E_COMMANDER_SSH_USER)", c.conf.ClusterName)
	}
	return conn.SSHHost, user, nil
}

func (c *connector) buildSSHClient(ctx context.Context, masterHost, masterUser string) (*ssh.Client, error) {
	master := ssh.Endpoint{
		User:       masterUser,
		Addr:       masterHost,
		KeyData:    c.creds.SSHKey,
		Passphrase: c.conf.SSHPassphrase,
	}
	hops := []ssh.Endpoint{master}
	if c.conf.JumpHostConfigured() {
		hops = []ssh.Endpoint{
			{
				User:       c.conf.SSHJumpUser,
				Addr:       c.conf.SSHJumpHost,
				KeyData:    c.creds.JumpKey,
				Passphrase: c.conf.SSHJumpPassphrase,
			},
			master,
		}
	}

	sshClient, err := ssh.NewWithRetry(ctx, ssh.Route(hops[0], hops[1:]...),
		connectRetryEvery, connectTimeout,
		ssh.WithInsecureIgnoreHostKey(), ssh.WithKeepalive(30*time.Second), ssh.WithLogger(c.logger))
	if err != nil {
		return nil, fmt.Errorf("creating ssh client: %w", err)
	}
	return sshClient, nil
}

// Connect resolves the master, opens an SSH tunnel to its API server, fetches
// the kubeconfig off the master, and returns a rest.Config pointed at the
// tunnel plus a cleanup that tears down the tunnel and SSH client (in reverse
// order). On error it releases any partially-acquired resources and returns a
// nil cleanup.
func (c *connector) Connect(ctx context.Context) (*rest.Config, func(), error) {
	host, user, err := c.resolveMaster(ctx)
	if err != nil {
		return nil, nil, err
	}
	c.logger.Info("connecting to Commander cluster",
		"cluster", c.conf.ClusterName,
		"master", fmt.Sprintf("%s@%s", user, host),
		"jumpHost", c.conf.SSHJumpHost,
	)

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	sshClient, err := c.buildSSHClient(ctx, host, user)
	if err != nil {
		return nil, nil, err
	}
	cleanups = append(cleanups, func() {
		if closeErr := sshClient.Close(); closeErr != nil {
			c.logger.Warn("failed to close ssh client", "err", closeErr)
		}
	})

	kubeconfig, err := c.fetchKubeconfig(ctx, sshClient)
	if err != nil {
		runCleanups()
		return nil, nil, err
	}

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

	restConfig, err := buildRestConfig(kubeconfig, tun.LocalAddr())
	if err != nil {
		runCleanups()
		return nil, nil, fmt.Errorf("creating rest config: %w", err)
	}

	return restConfig, runCleanups, nil
}

func (c *connector) fetchKubeconfig(ctx context.Context, sshClient *ssh.Client) ([]byte, error) {
	res, err := sshClient.Exec(ctx, getKubeconfigRemoteShell)
	if err != nil {
		return nil, fmt.Errorf("fetch kubeconfig over SSH (exit %d, stderr: %s): %w",
			res.ExitCode, strings.TrimSpace(string(res.Stderr)), err)
	}
	if len(res.Stdout) == 0 {
		return nil, fmt.Errorf("fetched kubeconfig is empty (stderr: %s)", strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

// Connect implements clusterprovider.Connector: it attaches a run to the
// Commander-managed cluster by resolving the SSH credentials and opening the
// connector (SSH to the master via the bastion, kubeconfig fetched off the
// master, in-process API tunnel), returning a rest.Config pointed at the tunnel
// plus a cleanup. The test suite connects through this instead of a legacy
// TEST_CLUSTER_CREATE_MODE path. Cancellation is detached so the tunnel outlives
// the caller's connect ctx (the suite keeps the connection for its whole run);
// cleanup tears it down.
func (p *commanderProvider) Connect(ctx context.Context) (*rest.Config, func(), error) {
	creds, err := p.conf.Resolve()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve commander credentials: %w", err)
	}
	return newConnector(p.client, p.conf, creds, p.logger).Connect(context.WithoutCancel(ctx))
}
