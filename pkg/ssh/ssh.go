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

// Package ssh exposes SSH client for use by external modules (e.g. sds-node-configurator e2e tests).
package ssh

import (
	"context"

	internalssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

// Client is the minimal SSH client interface needed for e2e helpers (Exec, Close).
type Client interface {
	Exec(ctx context.Context, cmd string) (string, error)
	Close() error
}

// NewClient creates a new SSH client (direct connection).
func NewClient(user, host, keyPath string) (Client, error) {
	return internalssh.NewClient(user, host, keyPath)
}

// NewClientWithJumpHost creates a new SSH client that connects through a jump host.
func NewClientWithJumpHost(jumpUser, jumpHost, jumpKeyPath, targetUser, targetHost, targetKeyPath string) (Client, error) {
	return internalssh.NewClientWithJumpHost(jumpUser, jumpHost, jumpKeyPath, targetUser, targetHost, targetKeyPath)
}
