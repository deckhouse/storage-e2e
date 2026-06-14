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

package dvp

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

// apiServerRemotePort is the port the cluster API server listens on. It is
// forwarded to an ephemeral local port through the SSH tunnel.
const apiServerRemotePort = "6445"

// sshEndpoint describes how to reach a host over SSH. When Jump is non-nil the
// connection is routed through it (jump -> target); otherwise it is direct.
type sshEndpoint struct {
	User    string
	Host    string
	KeyPath string
	Jump    *sshEndpoint
}

// dial opens an SSH connection to the endpoint, transparently routing through a
// jump host when one is configured.
func (e sshEndpoint) dial() (ssh.SSHClient, error) {
	if e.Jump != nil {
		return ssh.NewClientWithJumpHost(
			e.Jump.User, e.Jump.Host, e.Jump.KeyPath,
			e.User, e.Host, e.KeyPath,
		)
	}
	return ssh.NewClient(e.User, e.Host, e.KeyPath)
}

// clusterConnection is a live SSH tunnel to a cluster's API server together
// with the kubeconfig that targets it through that tunnel. Close releases both
// the tunnel and the SSH connection.
type clusterConnection struct {
	ssh    ssh.SSHClient
	tunnel *ssh.TunnelInfo

	// Kubeconfig is a client-go config whose server already points at the local
	// end of the tunnel; ready to use once openClusterConnection returns.
	Kubeconfig *rest.Config
	// KubeconfigPath is the on-disk kubeconfig (server rewritten to the local
	// tunnel port, mode 0600) for tooling such as kubectl/dhctl.
	KubeconfigPath string
}

// openClusterConnection connects to a (possibly closed) cluster over SSH,
// reads its kubeconfig from the control-plane node, forwards the API server
// through a local SSH tunnel, and returns a kubeconfig already pointing at that
// tunnel. On any failure all partially-acquired resources are released.
func openClusterConnection(ctx context.Context, ep sshEndpoint, kubeconfigDir string) (*clusterConnection, error) {
	sshClient, err := ep.dial()
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s@%s: %w", ep.User, ep.Host, err)
	}

	conn := &clusterConnection{ssh: sshClient}

	// The tunnel's lifetime is bound to ctx: it stops on ctx cancellation
	// (e.g. the Bootstrap deadline) or when Close is called explicitly.
	conn.tunnel, err = sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("establish API server tunnel: %w", err)
	}

	raw, err := fetchKubeconfig(ctx, sshClient)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("fetch kubeconfig from %s@%s: %w", ep.User, ep.Host, err)
	}

	path, err := kubeconfigFilePath(kubeconfigDir, ep.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	server := fmt.Sprintf("https://127.0.0.1:%d", conn.tunnel.LocalPort)
	conn.Kubeconfig, err = buildKubeconfig(raw, server, path)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}
	conn.KubeconfigPath = path

	return conn, nil
}

// Close stops the API server tunnel and closes the SSH connection, joining any
// errors. It is safe to call on a nil or partially-initialised connection.
func (c *clusterConnection) Close() error {
	if c == nil {
		return nil
	}

	var errs []error
	if c.tunnel != nil && c.tunnel.StopFunc != nil {
		if err := c.tunnel.StopFunc(); err != nil {
			errs = append(errs, fmt.Errorf("stop API server tunnel: %w", err))
		}
	}
	if c.ssh != nil {
		if err := c.ssh.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ssh client: %w", err))
		}
	}
	return errors.Join(errs...)
}
