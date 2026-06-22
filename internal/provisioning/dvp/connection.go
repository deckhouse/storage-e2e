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

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

const apiServerRemotePort = "6445"

type sshEndpoint struct {
	User    string
	Host    string
	KeyPath string
	Jump    *sshEndpoint
}

func (e sshEndpoint) dial() (ssh.SSHClient, error) {
	if e.Jump != nil {
		return ssh.NewClientWithJumpHost(
			e.Jump.User, e.Jump.Host, e.Jump.KeyPath,
			e.User, e.Host, e.KeyPath,
		)
	}
	return ssh.NewClient(e.User, e.Host, e.KeyPath)
}

type clusterConnection struct {
	ssh    ssh.SSHClient
	tunnel *ssh.TunnelInfo
}

func openTunnel(ctx context.Context, ep sshEndpoint) (*clusterConnection, error) {
	sshClient, err := ep.dial()
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s@%s: %w", ep.User, ep.Host, err)
	}

	conn := &clusterConnection{ssh: sshClient}

	conn.tunnel, err = sshClient.OpenTunnel(ctx, apiServerRemotePort)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("establish API server tunnel: %w", err)
	}

	return conn, nil
}

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
