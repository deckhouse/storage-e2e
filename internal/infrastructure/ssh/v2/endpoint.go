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

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Endpoint describes a single SSH host along a route: how to address it and how
// to authenticate to it. The zero value is not useful; at minimum User and Addr
// must be set, plus a usable credential (KeyPath, an ssh-agent, or both).
type Endpoint struct {
	// User is the login name.
	User string
	// Addr is "host" or "host:port"; the default port is 22.
	Addr string
	// KeyPath is the path to a private key file. A leading "~" is expanded to
	// the current user's home directory. It may be empty to rely solely on an
	// ssh-agent.
	KeyPath string
	// Passphrase decrypts an encrypted KeyPath. It is optional: when empty the
	// SSH_PASSPHRASE environment variable is consulted, and failing that the key
	// is skipped in favor of the ssh-agent.
	Passphrase string
	// HostKey verifies the server's host key. When nil the Client-level callback
	// (see WithHostKeyCallback) applies, defaulting to InsecureIgnoreHostKey.
	HostKey ssh.HostKeyCallback
}

// addr returns the dial address with a default :22 port when none is present.
func (e Endpoint) addr() string {
	if e.Addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(e.Addr); err == nil {
		return e.Addr
	}
	return net.JoinHostPort(e.Addr, "22")
}

// label is a short human-readable identity for logs and route descriptions.
func (e Endpoint) label() string {
	return fmt.Sprintf("%s@%s", e.User, e.addr())
}

// clientConfig builds the ssh.ClientConfig for this endpoint and returns an
// io.Closer that owns any ssh-agent connection opened for authentication. The
// caller (the route's connection chain) is responsible for closing it so the
// agent socket is not leaked on every reconnect. The closer is nil when no
// agent connection was opened.
func (e Endpoint) clientConfig(ctx context.Context, defaultHostKey ssh.HostKeyCallback) (*ssh.ClientConfig, io.Closer, error) {
	var signers []ssh.Signer

	if e.KeyPath != "" {
		keyPath, err := expandTilde(e.KeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve key path %q: %w", e.KeyPath, err)
		}
		raw, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read private key %q: %w", keyPath, err)
		}
		signer, err := parseSigner(raw, e.Passphrase)
		if err != nil {
			return nil, nil, fmt.Errorf("parse private key %q: %w", keyPath, err)
		}
		if signer != nil {
			signers = append(signers, signer)
		}
	}

	agentCloser := io.Closer(nil)
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		var dialer net.Dialer
		//nolint:gosec // G704: SSH_AUTH_SOCK is the standard, operator-controlled ssh-agent socket path.
		if conn, err := dialer.DialContext(ctx, "unix", sock); err == nil {
			if agentSigners, err := agent.NewClient(conn).Signers(); err == nil {
				signers = append(signers, agentSigners...)
			}
			// The connection must stay open for the agent signers to sign; the
			// route's chain closer owns and closes it.
			agentCloser = conn
		}
	}

	if len(signers) == 0 {
		return nil, nil, fmt.Errorf("no usable credentials for %s: set KeyPath or start an ssh-agent", e.label())
	}

	hostKey := e.HostKey
	if hostKey == nil {
		hostKey = defaultHostKey
	}
	if hostKey == nil {
		//nolint:gosec // G106: last-resort default for ephemeral e2e VMs; overridable per Endpoint or via WithHostKeyCallback.
		hostKey = ssh.InsecureIgnoreHostKey()
	}

	cfg := &ssh.ClientConfig{
		User:            e.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKey,
		Timeout:         defaultDialTimeout,
	}
	return cfg, agentCloser, nil
}

// parseSigner parses a private key, transparently handling passphrase-protected
// keys. When the key is encrypted but no passphrase is available (neither the
// explicit value nor SSH_PASSPHRASE), it returns (nil, nil) so the caller falls
// back to the ssh-agent. Passphrase protection is detected structurally via
// *ssh.PassphraseMissingError, not by inspecting error text.
func parseSigner(raw []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(raw)
	if err == nil {
		return signer, nil
	}

	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, err
	}

	pass := passphrase
	if pass == "" {
		pass = os.Getenv("SSH_PASSPHRASE")
	}
	if pass == "" {
		// Encrypted key with no passphrase: defer to the ssh-agent fallback.
		return nil, nil
	}

	signer, err = ssh.ParsePrivateKeyWithPassphrase(raw, []byte(pass))
	if err != nil {
		return nil, fmt.Errorf("decrypt private key with passphrase: %w", err)
	}
	return signer, nil
}

// expandTilde expands a leading "~" or "~/" to the current user's home dir.
func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("look up current user: %w", err)
	}
	if path == "~" {
		return usr.HomeDir, nil
	}
	return filepath.Join(usr.HomeDir, strings.TrimPrefix(path, "~/")), nil
}
