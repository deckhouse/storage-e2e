/*
Copyright 2025 Flant JSC

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

package ssh

import "context"

// SSHClient provides SSH operations
type SSHClient interface {
	// Create creates a new SSH client
	Create(user, host, keyPath string) (SSHClient, error)

	// CreateForward creates an SSH client with port forwarding
	CreateForward(user, host, keyPath string, localPort, remotePort string) (SSHClient, error)

	// Exec executes a command on the remote host
	Exec(ctx context.Context, cmd string) (string, error)

	// ExecFatal executes a command and returns error if it fails
	ExecFatal(ctx context.Context, cmd string) string

	// Uploads a local file to the remote host
	Upload(ctx context.Context, localPath, remotePath string) error

	// Close closes the SSH connection
	Close() error
}

// Factory provides a way to create SSH clients
type SSHFactory interface {
	CreateClient(user, host, keyPath string) (SSHClient, error)
}
