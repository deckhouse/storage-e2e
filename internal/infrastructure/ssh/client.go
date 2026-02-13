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
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// sshKeyInfo holds metadata about the SSH key used for authentication.
// This is used to produce diagnostic log messages and enriched error messages
// when authentication fails.
type sshKeyInfo struct {
	Path        string // Resolved filesystem path of the private key
	Algorithm   string // Key algorithm (e.g. ssh-ed25519, ssh-rsa, ecdsa-sha2-nistp256)
	Fingerprint string // SHA256 fingerprint of the public key
}

// client implements Client interface
type client struct {
	mu              sync.Mutex
	sshClient       *ssh.Client
	keepaliveCtx    context.Context
	keepaliveCancel context.CancelFunc
	keepaliveWg     sync.WaitGroup
	// Connection parameters for reconnection
	user    string
	host    string
	keyPath string
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

// getSSHPrivateKeyPath handles both file path and base64-encoded private key
// If keyPathOrBase64 is a base64 string, it decodes and writes to a temp file
// If it's a path, it expands ~ and returns the path
func getSSHPrivateKeyPath(keyPathOrBase64 string) (string, error) {
	// Check if it looks like a file path (contains path separators or starts with ~)
	looksLikePath := strings.Contains(keyPathOrBase64, "/") || strings.HasPrefix(keyPathOrBase64, "~") || strings.Contains(keyPathOrBase64, "\\")

	if !looksLikePath {
		// Doesn't look like a path, try base64 decoding
		decoded, err := base64.StdEncoding.DecodeString(keyPathOrBase64)
		if err == nil && len(decoded) > 0 {
			// Successfully decoded, write to temp file
			tmpFile, err := os.CreateTemp("", "ssh_private_key_*")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file for private key: %w", err)
			}
			defer tmpFile.Close()

			if _, err := tmpFile.Write(decoded); err != nil {
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("failed to write decoded private key to temp file: %w", err)
			}

			// Set permissions to 0600
			if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
				os.Remove(tmpFile.Name())
				return "", fmt.Errorf("failed to set permissions on temp private key file: %w", err)
			}

			return tmpFile.Name(), nil
		}
		// If decoding failed, fall through to treat as path (might be a relative path without /)
	}

	// Treat as file path
	return expandPath(keyPathOrBase64)
}

// createSSHConfig creates SSH client config with support for passphrase-protected keys.
// It returns the SSH client config, key metadata (for diagnostics), and an error.
// The key metadata includes the resolved path, algorithm, and SHA256 fingerprint of the key,
// which is useful for debugging authentication failures.
func createSSHConfig(user, keyPathOrBase64 string) (*ssh.ClientConfig, *sshKeyInfo, error) {
	// keyPathOrBase64 can be either a file path or a base64-encoded private key
	// Use GetSSHPrivateKeyPath to handle both cases
	expandedKeyPath, err := getSSHPrivateKeyPath(keyPathOrBase64)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get private key path: %w", err)
	}

	key, err := os.ReadFile(expandedKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to read private key %s: %w", expandedKeyPath, err)
	}

	// Always try parsing without passphrase first
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		// Only if the error specifically indicates passphrase protection, try with passphrase
		if !strings.Contains(err.Error(), "ssh: this private key is passphrase protected") {
			return nil, nil, fmt.Errorf("unable to parse private key '%s': %w", expandedKeyPath, err)
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
				return nil, nil, fmt.Errorf("SSH key '%s' is passphrase protected. Set SSH_PASSPHRASE environment variable: export SSH_PASSPHRASE='your-passphrase'\nOriginal error: %w", expandedKeyPath, readErr)
			}
		}

		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, pass)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to parse private key '%s' with passphrase: %w", expandedKeyPath, err)
		}
	}

	// Collect key metadata for diagnostics
	keyInfo := &sshKeyInfo{
		Path:        expandedKeyPath,
		Algorithm:   signer.PublicKey().Type(),
		Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}
	logger.Debug("SSH key loaded: path=%s, algorithm=%s, fingerprint=%s", keyInfo.Path, keyInfo.Algorithm, keyInfo.Fingerprint)

	return &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, keyInfo, nil
}

// Create creates a new SSH client
func (c *client) Create(user, host, keyPath string) (SSHClient, error) {
	sshConfig, keyInfo, err := createSSHConfig(user, keyPath)
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
		return nil, fmt.Errorf("failed to connect to %s@%s: %w\n  Key used: %s (algorithm: %s, fingerprint: %s)\n  Hint: verify this key's public part is in authorized_keys on the server",
			user, addr, err, keyInfo.Path, keyInfo.Algorithm, keyInfo.Fingerprint)
	}

	// Start keepalive mechanism (equivalent to ServerAliveInterval=60)
	keepaliveCtx, keepaliveCancel := context.WithCancel(context.Background())
	newClient := &client{
		sshClient:       sshClient,
		keepaliveCtx:    keepaliveCtx,
		keepaliveCancel: keepaliveCancel,
		// Store connection parameters for reconnection
		user:    user,
		host:    host,
		keyPath: keyPath,
	}
	newClient.startKeepalive()

	return newClient, nil
}

// startKeepalive starts a goroutine that sends keepalive requests every 60 seconds
// This prevents SSH connections from timing out due to inactivity.
// Note: golang.org/x/crypto/ssh doesn't have a built-in keepalive parameter,
// so we implement it manually using SendRequest with "keepalive@openssh.com"
// (equivalent to ServerAliveInterval=60 in SSH config)
func (c *client) startKeepalive() {
	c.keepaliveWg.Add(1)
	go func() {
		defer c.keepaliveWg.Done()
		ticker := time.NewTicker(config.SSHKeepaliveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.keepaliveCtx.Done():
				return
			case <-ticker.C:
				// Send keepalive request using standard OpenSSH keepalive request type
				// This is equivalent to ServerAliveInterval in SSH config
				_, _, err := c.sshClient.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					// Connection is closed, stop sending keepalives
					return
				}
			}
		}
	}()
}

// isConnectionError checks if the error indicates a broken SSH connection
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	errStr := err.Error()
	connectionPatterns := []string{
		"failed to create SSH session",
		"ssh: handshake failed",
		"ssh: connection lost",
		"use of closed network connection",
		"connection refused",
		"connection reset",
		"broken pipe",
		"EOF",
		"i/o timeout",
	}
	for _, pattern := range connectionPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	return false
}

// reconnect attempts to re-establish the SSH connection
func (c *client) reconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stop old keepalive
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
		c.keepaliveWg.Wait()
	}

	// Close old connection
	if c.sshClient != nil {
		_ = c.sshClient.Close()
	}

	// Reconnect with retry
	retryDelay := config.SSHRetryInitialDelay
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
				retryDelay *= 2
				if retryDelay > config.SSHRetryMaxDelay {
					retryDelay = config.SSHRetryMaxDelay
				}
			}
		}

		sshConfig, keyInfo, err := createSSHConfig(c.user, c.keyPath)
		if err != nil {
			lastErr = err
			continue
		}

		addr := c.host
		if !strings.Contains(addr, ":") {
			addr = addr + ":22"
		}

		sshClient, err := ssh.Dial("tcp", addr, sshConfig)
		if err != nil {
			lastErr = fmt.Errorf("failed to connect to %s@%s: %w\n  Key used: %s (algorithm: %s, fingerprint: %s)\n  Hint: verify this key's public part is in authorized_keys on the server",
				c.user, addr, err, keyInfo.Path, keyInfo.Algorithm, keyInfo.Fingerprint)
			continue
		}

		// Success - update client
		c.sshClient = sshClient
		c.keepaliveCtx, c.keepaliveCancel = context.WithCancel(context.Background())
		c.startKeepalive()
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts: %w", config.SSHRetryCount, lastErr)
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

// Exec executes a command on the remote host with automatic retry and reconnection
func (c *client) Exec(ctx context.Context, cmd string) (string, error) {
	var output string
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		// Check context before starting
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("context error before execution: %w", err)
		}

		c.mu.Lock()
		sshClient := c.sshClient
		c.mu.Unlock()

		session, err := sshClient.NewSession()
		if err != nil {
			lastErr = fmt.Errorf("failed to create SSH session: %w", err)
			if isConnectionError(err) {
				// Try to reconnect
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return "", fmt.Errorf("SSH session failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue // Retry with new connection
			}
			return "", lastErr
		}

		output, err := session.CombinedOutput(cmd)
		session.Close()

		if err != nil {
			// Check if context was cancelled
			if ctx.Err() != nil {
				return string(output), fmt.Errorf("context cancelled: %w", ctx.Err())
			}

			lastErr = fmt.Errorf("command failed: %w", err)

			// Check if it's a connection error that might benefit from reconnection
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return string(output), fmt.Errorf("command failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue // Retry with new connection
			}

			// Non-connection error (actual command failure) - don't retry
			return string(output), lastErr
		}

		// Check context after execution
		if err := ctx.Err(); err != nil {
			return string(output), fmt.Errorf("context cancelled: %w", err)
		}

		return string(output), nil
	}

	return output, fmt.Errorf("SSH exec failed after %d attempts: %w", config.SSHRetryCount, lastErr)
}

// ExecFatal executes a command and returns error if it fails
func (c *client) ExecFatal(ctx context.Context, cmd string) string {
	output, err := c.Exec(ctx, cmd)
	if err != nil {
		panic(fmt.Sprintf("ExecFatal failed for command '%s': %v\nOutput: %s", cmd, err, output))
	}
	return output
}

// Upload uploads a local file to the remote host with automatic retry and reconnection
func (c *client) Upload(ctx context.Context, localPath, remotePath string) error {
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		// Check context before starting
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context error before upload: %w", err)
		}

		c.mu.Lock()
		sshClient := c.sshClient
		c.mu.Unlock()

		sftpClient, err := sftp.NewClient(sshClient)
		if err != nil {
			lastErr = fmt.Errorf("failed to create SFTP client: %w", err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("SFTP client creation failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		localFile, err := os.Open(localPath)
		if err != nil {
			sftpClient.Close()
			return fmt.Errorf("failed to open local file %s: %w", localPath, err)
		}

		remoteFile, err := sftpClient.Create(remotePath)
		if err != nil {
			localFile.Close()
			sftpClient.Close()
			lastErr = fmt.Errorf("failed to create remote file %s: %w", remotePath, err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("remote file creation failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		// Use context-aware copy
		_, err = copyWithContext(ctx, remoteFile, localFile)
		remoteFile.Close()
		localFile.Close()
		sftpClient.Close()

		if err != nil {
			lastErr = fmt.Errorf("failed to copy file: %w", err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("file copy failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		return nil
	}

	return fmt.Errorf("SSH upload failed after %d attempts: %w", config.SSHRetryCount, lastErr)
}

// Close closes the SSH connection
func (c *client) Close() error {
	// Stop keepalive goroutine
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
		c.keepaliveWg.Wait()
	}
	if c.sshClient != nil {
		err := c.sshClient.Close()
		// Ignore EOF errors - they just mean the connection was already closed
		if err != nil && (errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF")) {
			return nil
		}
		return err
	}
	return nil
}

// NewClient creates a new SSH client
func NewClient(user, host, keyPath string) (SSHClient, error) {
	var c client
	return c.Create(user, host, keyPath)
}

// NewClientWithJumpHost creates a new SSH client that connects through a jump host
// It first connects to the jump host, then establishes a connection to the target host through it
func NewClientWithJumpHost(jumpUser, jumpHost, jumpKeyPath, targetUser, targetHost, targetKeyPath string) (SSHClient, error) {
	// Create SSH config for jump host
	jumpConfig, jumpKeyInfo, err := createSSHConfig(jumpUser, jumpKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH config for jump host: %w", err)
	}

	// Ensure jump host has port if not specified
	jumpAddr := jumpHost
	if !strings.Contains(jumpAddr, ":") {
		jumpAddr = jumpAddr + ":22"
	}

	// Connect to jump host
	jumpClient, err := ssh.Dial("tcp", jumpAddr, jumpConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to jump host %s@%s: %w\n  Key used: %s (algorithm: %s, fingerprint: %s)\n  Hint: verify this key's public part is in authorized_keys on the server",
			jumpUser, jumpAddr, err, jumpKeyInfo.Path, jumpKeyInfo.Algorithm, jumpKeyInfo.Fingerprint)
	}

	// Create SSH config for target host
	targetConfig, _, err := createSSHConfig(targetUser, targetKeyPath)
	if err != nil {
		jumpClient.Close()
		return nil, fmt.Errorf("failed to create SSH config for target host: %w", err)
	}

	// Ensure target host has port if not specified
	targetAddr := targetHost
	if !strings.Contains(targetAddr, ":") {
		targetAddr = targetAddr + ":22"
	}

	// Connect to target host through jump host with retry
	maxRetries := config.SSHRetryCount
	retryDelay := config.SSHRetryInitialDelay
	var targetClient *ssh.Client
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
			if retryDelay > config.SSHRetryMaxDelay {
				retryDelay = config.SSHRetryMaxDelay
			}
		}

		targetConn, err := jumpClient.Dial("tcp", targetAddr)
		if err != nil {
			lastErr = fmt.Errorf("failed to dial target host %s@%s through jump host: %w", targetUser, targetAddr, err)
			continue
		}

		targetClientConn, targetChans, targetReqs, err := ssh.NewClientConn(targetConn, targetAddr, targetConfig)
		if err != nil {
			targetConn.Close()
			lastErr = fmt.Errorf("failed to establish SSH connection to target host: %w", err)
			continue
		}

		targetClient = ssh.NewClient(targetClientConn, targetChans, targetReqs)
		break
	}

	if targetClient == nil {
		jumpClient.Close()
		return nil, fmt.Errorf("failed to connect to target host after %d attempts: %w", maxRetries, lastErr)
	}

	// Start keepalive for both connections
	keepaliveCtx, keepaliveCancel := context.WithCancel(context.Background())

	// Return a client that wraps both connections
	// When closing, we need to close both connections
	jc := &jumpHostClient{
		jumpClient:      jumpClient,
		targetClient:    targetClient,
		keepaliveCtx:    keepaliveCtx,
		keepaliveCancel: keepaliveCancel,
		// Store connection parameters for reconnection
		jumpUser:      jumpUser,
		jumpHost:      jumpHost,
		jumpKeyPath:   jumpKeyPath,
		targetUser:    targetUser,
		targetHost:    targetHost,
		targetKeyPath: targetKeyPath,
	}
	jc.startKeepalive()
	return jc, nil
}

// jumpHostClient wraps both jump host and target client connections
type jumpHostClient struct {
	mu           sync.Mutex
	jumpClient   *ssh.Client
	targetClient *ssh.Client
	// Keepalive for both connections
	keepaliveCtx    context.Context
	keepaliveCancel context.CancelFunc
	keepaliveWg     sync.WaitGroup
	// Connection parameters for reconnection
	jumpUser      string
	jumpHost      string
	jumpKeyPath   string
	targetUser    string
	targetHost    string
	targetKeyPath string
}

// startKeepalive starts goroutines that send keepalive requests to both connections
func (c *jumpHostClient) startKeepalive() {
	// Keepalive for jump client
	c.keepaliveWg.Add(1)
	go func() {
		defer c.keepaliveWg.Done()
		ticker := time.NewTicker(config.SSHKeepaliveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.keepaliveCtx.Done():
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.jumpClient != nil {
					_, _, _ = c.jumpClient.SendRequest("keepalive@openssh.com", true, nil)
				}
				c.mu.Unlock()
			}
		}
	}()

	// Keepalive for target client
	c.keepaliveWg.Add(1)
	go func() {
		defer c.keepaliveWg.Done()
		ticker := time.NewTicker(config.SSHKeepaliveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.keepaliveCtx.Done():
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.targetClient != nil {
					_, _, _ = c.targetClient.SendRequest("keepalive@openssh.com", true, nil)
				}
				c.mu.Unlock()
			}
		}
	}()
}

// Create creates a new SSH client (not used for jump host client)
func (c *jumpHostClient) Create(user, host, keyPath string) (SSHClient, error) {
	return nil, fmt.Errorf("Create not supported for jump host client")
}

// reconnect attempts to re-establish the SSH connection through jump host
func (c *jumpHostClient) reconnect(ctx context.Context) error {
	// Stop old keepalives (outside lock to avoid deadlock)
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
		c.keepaliveWg.Wait()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Close old connections
	if c.targetClient != nil {
		_ = c.targetClient.Close()
	}
	if c.jumpClient != nil {
		_ = c.jumpClient.Close()
	}

	retryDelay := config.SSHRetryInitialDelay
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
				retryDelay *= 2
				if retryDelay > config.SSHRetryMaxDelay {
					retryDelay = config.SSHRetryMaxDelay
				}
			}
		}

		// Reconnect to jump host
		jumpConfig, jumpKeyInfo, err := createSSHConfig(c.jumpUser, c.jumpKeyPath)
		if err != nil {
			lastErr = err
			continue
		}

		jumpAddr := c.jumpHost
		if !strings.Contains(jumpAddr, ":") {
			jumpAddr = jumpAddr + ":22"
		}

		jumpClient, err := ssh.Dial("tcp", jumpAddr, jumpConfig)
		if err != nil {
			lastErr = fmt.Errorf("failed to reconnect to jump host: %w\n  Key used: %s (algorithm: %s, fingerprint: %s)\n  Hint: verify this key's public part is in authorized_keys on the server",
				err, jumpKeyInfo.Path, jumpKeyInfo.Algorithm, jumpKeyInfo.Fingerprint)
			continue
		}

		// Reconnect to target through jump host
		targetConfig, _, err := createSSHConfig(c.targetUser, c.targetKeyPath)
		if err != nil {
			jumpClient.Close()
			lastErr = err
			continue
		}

		targetAddr := c.targetHost
		if !strings.Contains(targetAddr, ":") {
			targetAddr = targetAddr + ":22"
		}

		targetConn, err := jumpClient.Dial("tcp", targetAddr)
		if err != nil {
			jumpClient.Close()
			lastErr = fmt.Errorf("failed to dial target through jump host: %w", err)
			continue
		}

		targetClientConn, targetChans, targetReqs, err := ssh.NewClientConn(targetConn, targetAddr, targetConfig)
		if err != nil {
			targetConn.Close()
			jumpClient.Close()
			lastErr = fmt.Errorf("failed to establish target connection: %w", err)
			continue
		}

		// Success - update clients
		c.jumpClient = jumpClient
		c.targetClient = ssh.NewClient(targetClientConn, targetChans, targetReqs)
		c.keepaliveCtx, c.keepaliveCancel = context.WithCancel(context.Background())
		// Unlock before starting keepalive to avoid deadlock (keepalive goroutines acquire lock)
		c.mu.Unlock()
		c.startKeepalive()
		c.mu.Lock() // Re-lock for deferred unlock
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts: %w", config.SSHRetryCount, lastErr)
}

// StartTunnel starts an SSH tunnel with port forwarding from local to remote
func (c *jumpHostClient) StartTunnel(ctx context.Context, localPort, remotePort string) (func() error, error) {
	// Use the target client's StartTunnel method
	// We need to access the underlying client's StartTunnel
	// Since we can't directly call it, we'll implement it here
	return startTunnelOnClient(ctx, c.targetClient, localPort, remotePort)
}

// startTunnelOnClient starts a tunnel on a raw ssh.Client
func startTunnelOnClient(ctx context.Context, sshClient *ssh.Client, localPort, remotePort string) (func() error, error) {
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
				if tcpListener, ok := listener.(*net.TCPListener); ok {
					if err := tcpListener.SetDeadline(deadline); err != nil {
						// If setting deadline fails, continue without it
					}
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
				remoteConn, err := sshClient.Dial("tcp", "127.0.0.1:"+remotePort)
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

// Exec executes a command on the remote host with automatic retry and reconnection
func (c *jumpHostClient) Exec(ctx context.Context, cmd string) (string, error) {
	var output string
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		// Check context before starting
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("context error before execution: %w", err)
		}

		c.mu.Lock()
		targetClient := c.targetClient
		c.mu.Unlock()

		session, err := targetClient.NewSession()
		if err != nil {
			lastErr = fmt.Errorf("failed to create SSH session: %w", err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return "", fmt.Errorf("SSH session failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return "", lastErr
		}

		output, err := session.CombinedOutput(cmd)
		session.Close()

		if err != nil {
			// Check if context was cancelled
			if ctx.Err() != nil {
				return string(output), fmt.Errorf("context cancelled: %w", ctx.Err())
			}

			lastErr = fmt.Errorf("command failed: %w", err)

			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return string(output), fmt.Errorf("command failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}

			// Non-connection error - don't retry
			return string(output), lastErr
		}

		// Check context after execution
		if err := ctx.Err(); err != nil {
			return string(output), fmt.Errorf("context cancelled: %w", err)
		}

		return string(output), nil
	}

	return output, fmt.Errorf("SSH exec failed after %d attempts: %w", config.SSHRetryCount, lastErr)
}

// ExecFatal executes a command and returns error if it fails
func (c *jumpHostClient) ExecFatal(ctx context.Context, cmd string) string {
	output, err := c.Exec(ctx, cmd)
	if err != nil {
		panic(fmt.Sprintf("ExecFatal failed for command '%s': %v\nOutput: %s", cmd, err, output))
	}
	return output
}

// Upload uploads a local file to the remote host with automatic retry and reconnection
func (c *jumpHostClient) Upload(ctx context.Context, localPath, remotePath string) error {
	var lastErr error

	for attempt := 0; attempt < config.SSHRetryCount; attempt++ {
		// Check context before starting
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context error before upload: %w", err)
		}

		c.mu.Lock()
		targetClient := c.targetClient
		c.mu.Unlock()

		sftpClient, err := sftp.NewClient(targetClient)
		if err != nil {
			lastErr = fmt.Errorf("failed to create SFTP client: %w", err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("SFTP client creation failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		localFile, err := os.Open(localPath)
		if err != nil {
			sftpClient.Close()
			return fmt.Errorf("failed to open local file %s: %w", localPath, err)
		}

		remoteFile, err := sftpClient.Create(remotePath)
		if err != nil {
			localFile.Close()
			sftpClient.Close()
			lastErr = fmt.Errorf("failed to create remote file %s: %w", remotePath, err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("remote file creation failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		// Use context-aware copy
		_, err = copyWithContext(ctx, remoteFile, localFile)
		remoteFile.Close()
		localFile.Close()
		sftpClient.Close()

		if err != nil {
			lastErr = fmt.Errorf("failed to copy file: %w", err)
			if isConnectionError(err) {
				if reconnErr := c.reconnect(ctx); reconnErr != nil {
					return fmt.Errorf("file copy failed and reconnection failed: %w (original: %v)", reconnErr, lastErr)
				}
				continue
			}
			return lastErr
		}

		return nil
	}

	return fmt.Errorf("SSH upload failed after %d attempts: %w", config.SSHRetryCount, lastErr)
}

// Close closes both SSH connections
func (c *jumpHostClient) Close() error {
	// Stop keepalive goroutines first
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
		c.keepaliveWg.Wait()
	}

	var errs []error
	if c.targetClient != nil {
		if err := c.targetClient.Close(); err != nil {
			// Ignore EOF errors - they just mean the connection was already closed
			if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "EOF") {
				errs = append(errs, err)
			}
		}
	}
	if c.jumpClient != nil {
		if err := c.jumpClient.Close(); err != nil {
			// Ignore EOF errors - they just mean the connection was already closed
			if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "EOF") {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %v", errs)
	}
	return nil
}
