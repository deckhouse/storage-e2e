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
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// client implements Client interface
type client struct {
	sshClient *ssh.Client
}

// copyWithContext copies data from src to dst with context cancellation support
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (written int64, err error) {
	buf := make([]byte, 32*1024)
	for {
		// Check context before each read
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = fmt.Errorf("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

// readPassword reads a password from the terminal
func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	var fd int
	if term.IsTerminal(syscall.Stdin) {
		fd = syscall.Stdin
	} else {
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return nil, fmt.Errorf("error allocating terminal: %w", err)
		}
		defer tty.Close()
		fd = int(tty.Fd())
	}

	pass, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return pass, err
}

// expandPath expands ~ to home directory
func expandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	if path == "~" {
		return usr.HomeDir, nil
	}

	return filepath.Join(usr.HomeDir, strings.TrimPrefix(path, "~/")), nil
}

// createSSHConfig creates SSH client config with support for passphrase-protected keys
func createSSHConfig(user, keyPath string) (*ssh.ClientConfig, error) {
	expandedKeyPath, err := expandPath(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to expand key path: %w", err)
	}

	key, err := os.ReadFile(expandedKeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key %s: %w", expandedKeyPath, err)
	}

	// Always try parsing without passphrase first
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		// Only if the error specifically indicates passphrase protection, try with passphrase
		if !strings.Contains(err.Error(), "ssh: this private key is passphrase protected") {
			return nil, fmt.Errorf("unable to parse private key: %w", err)
		}

		// Key is passphrase-protected, get passphrase
		var pass []byte
		if envPass := os.Getenv("SSH_PASSPHRASE"); envPass != "" {
			pass = []byte(envPass)
		} else {
			// Try to read from terminal
			var readErr error
			pass, readErr = readPassword("    Enter passphrase for '" + expandedKeyPath + "': ")
			if readErr != nil {
				return nil, fmt.Errorf("SSH key '%s' is passphrase protected. Set SSH_PASSPHRASE environment variable: export SSH_PASSPHRASE='your-passphrase'\nOriginal error: %w", expandedKeyPath, readErr)
			}
		}

		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, pass)
		if err != nil {
			return nil, fmt.Errorf("unable to parse private key with passphrase: %w", err)
		}
	}

	return &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, nil
}

// Create creates a new SSH client
func (c *client) Create(user, host, keyPath string) (SSHClient, error) {
	sshConfig, err := createSSHConfig(user, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH config: %w", err)
	}

	// Ensure host has port if not specified
	addr := host
	if !strings.Contains(addr, ":") {
		addr = addr + ":22"
	}

	sshClient, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s@%s: %w", user, addr, err)
	}

	return &client{sshClient: sshClient}, nil
}

// StartTunnel starts an SSH tunnel with port forwarding from local to remote
// It returns a function to stop the tunnel and an error if the tunnel fails to start
func (c *client) StartTunnel(ctx context.Context, localPort, remotePort string) (func() error, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before starting tunnel: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on local port %s: %w", localPort, err)
	}

	stopChan := make(chan struct{})

	go func() {
		defer listener.Close()
		for {
			// Check context and stop channel
			select {
			case <-ctx.Done():
				return
			case <-stopChan:
				return
			default:
			}

			// Set deadline for Accept based on context deadline if available
			if deadline, ok := ctx.Deadline(); ok {
				if err := listener.(*net.TCPListener).SetDeadline(deadline); err != nil {
					// If setting deadline fails, continue without it
				}
			}

			localConn, err := listener.Accept()
			if err != nil {
				// Listener closed or error occurred
				select {
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				default:
					// Continue if not stopped
					continue
				}
			}

			go func() {
				defer localConn.Close()
				remoteConn, err := c.sshClient.Dial("tcp", "127.0.0.1:"+remotePort)
				if err != nil {
					// Connection failed, just return - the error will be visible to the client
					return
				}
				defer remoteConn.Close()

				// Copy data bidirectionally with context support
				done := make(chan struct{}, 2)
				go func() {
					_, _ = copyWithContext(ctx, localConn, remoteConn)
					done <- struct{}{}
				}()
				go func() {
					_, _ = copyWithContext(ctx, remoteConn, localConn)
					done <- struct{}{}
				}()

				// Wait for either direction to finish or context cancellation
				select {
				case <-ctx.Done():
					return
				case <-done:
					// One direction finished, wait for the other
					select {
					case <-ctx.Done():
						return
					case <-done:
						// Both directions finished
					}
				}
			}()
		}
	}()

	stop := func() error {
		close(stopChan)
		return listener.Close()
	}

	return stop, nil
}

// Exec executes a command on the remote host
func (c *client) Exec(ctx context.Context, cmd string) (string, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context error before execution: %w", err)
	}

	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	// Note: session.CombinedOutput doesn't support context directly,
	// but we check context before and after the call
	// For better cancellation support, consider using session.Start() with context-aware goroutines
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		// Check if context was cancelled during execution
		if ctx.Err() != nil {
			return string(output), fmt.Errorf("context cancelled: %w", ctx.Err())
		}
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	// Check context after execution
	if err := ctx.Err(); err != nil {
		return string(output), fmt.Errorf("context cancelled: %w", err)
	}

	return string(output), nil
}

// ExecFatal executes a command and returns error if it fails
func (c *client) ExecFatal(ctx context.Context, cmd string) string {
	output, err := c.Exec(ctx, cmd)
	if err != nil {
		panic(fmt.Sprintf("ExecFatal failed for command '%s': %v\nOutput: %s", cmd, err, output))
	}
	return output
}

// Upload uploads a local file to the remote host
func (c *client) Upload(ctx context.Context, localPath, remotePath string) error {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context error before upload: %w", err)
	}

	sftpClient, err := sftp.NewClient(c.sshClient)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer localFile.Close()

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file %s: %w", remotePath, err)
	}
	defer remoteFile.Close()

	// Use context-aware copy
	_, err = copyWithContext(ctx, remoteFile, localFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// Close closes the SSH connection
func (c *client) Close() error {
	if c.sshClient != nil {
		return c.sshClient.Close()
	}
	return nil
}

// NewClient creates a new SSH client
func NewClient(user, host, keyPath string) (SSHClient, error) {
	var c client
	return c.Create(user, host, keyPath)
}
