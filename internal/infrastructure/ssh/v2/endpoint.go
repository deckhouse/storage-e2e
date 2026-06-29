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

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Endpoint struct {
	User       string
	Addr       string
	KeyData    []byte
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

	if len(e.KeyData) > 0 {
		signer, err := parseSigner(e.KeyData, e.Passphrase)
		if err != nil {
			return nil, nil, fmt.Errorf("parse private key for %s: %w", e.label(), err)
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
		return nil, nil, fmt.Errorf("no usable credentials for %s: set KeyData or start an ssh-agent", e.label())
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
