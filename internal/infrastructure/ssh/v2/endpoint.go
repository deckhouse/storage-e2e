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

type Endpoint struct {
	User       string
	Addr       string
	KeyPath    string
	Passphrase string
	HostKey    ssh.HostKeyCallback
}

func (e Endpoint) addr() string {
	if e.Addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(e.Addr); err == nil {
		return e.Addr
	}
	return net.JoinHostPort(e.Addr, "22")
}

func (e Endpoint) label() string {
	return fmt.Sprintf("%s@%s", e.User, e.addr())
}

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
		if conn, err := dialer.DialContext(ctx, "unix", sock); err == nil {
			if agentSigners, err := agent.NewClient(conn).Signers(); err == nil {
				signers = append(signers, agentSigners...)
			}
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

	cfg := &ssh.ClientConfig{
		User:            e.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKey,
		Timeout:         defaultDialTimeout,
	}
	return cfg, agentCloser, nil
}

func parseSigner(raw []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(raw)
	if err == nil {
		return signer, nil
	}

	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); !ok {
		return nil, err
	}

	if passphrase == "" {
		return nil, nil
	}

	signer, err = ssh.ParsePrivateKeyWithPassphrase(raw, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("decrypt private key with passphrase: %w", err)
	}
	return signer, nil
}

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
