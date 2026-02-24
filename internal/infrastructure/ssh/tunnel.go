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
	netstd "net"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// StartTunnel starts an SSH tunnel with port forwarding from local to remote
// It returns a function to stop the tunnel and an error if the tunnel fails to start
func StartTunnel(ctx context.Context, sshClient *ssh.Client, localPort, remotePort string) (func() error, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before starting tunnel: %w", err)
	}

	listener, err := netstd.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on local port %s: %w", localPort, err)
	}

	stopChan := make(chan struct{})
	var connectionErrors int64 // Track connection errors for logging

	logger.Debug("SSH tunnel started: local:%s -> remote:%s", localPort, remotePort)

	go func() {
		defer listener.Close()
		for {
			// Check context and stop channel
			select {
			case <-ctx.Done():
				logger.Debug("SSH tunnel stopped (context done): local:%s -> remote:%s", localPort, remotePort)
				return
			case <-stopChan:
				logger.Debug("SSH tunnel stopped (stop signal): local:%s -> remote:%s", localPort, remotePort)
				return
			default:
			}

			// Set deadline for Accept based on context deadline if available
			if tcpListener, ok := listener.(*netstd.TCPListener); ok {
				if deadline, ok := ctx.Deadline(); ok {
					if err := tcpListener.SetDeadline(deadline); err != nil {
						// If setting deadline fails, continue without it
					}
				} else {
					// Set a short deadline to allow periodic context checking
					if err := tcpListener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
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
					// Continue if not stopped (this is normal for timeout-based accept)
					continue
				}
			}

			go func() {
				defer localConn.Close()
				remoteConn, err := sshClient.Dial("tcp", "127.0.0.1:"+remotePort)
				if err != nil {
					// Log connection errors (but don't spam - only log every 10th error)
					errCount := atomic.AddInt64(&connectionErrors, 1)
					if errCount == 1 || errCount%10 == 0 {
						logger.Warn("SSH tunnel connection error (count: %d): failed to dial remote port %s: %v",
							errCount, remotePort, err)
					}
					return
				}
				defer remoteConn.Close()

				// Reset error counter on successful connection
				atomic.StoreInt64(&connectionErrors, 0)

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

// EstablishSSHTunnel establishes an SSH tunnel with port forwarding from remote node to local port on the client
// It automatically finds a free local port to avoid conflicts when running parallel tests.
// Uses retry logic for transient connection errors.
// Returns the tunnel info, local port and error if the tunnel fails to start
func EstablishSSHTunnel(ctx context.Context, sshClient SSHClient, remotePort string) (*TunnelInfo, error) {
	// Parse remote port to integer
	remotePortInt, err := strconv.Atoi(remotePort)
	if err != nil {
		return nil, fmt.Errorf("invalid remote port %s: %w", remotePort, err)
	}

	// Retry configuration for tunnel establishment using centralized SSH config
	maxRetries := config.SSHRetryCount
	retryDelay := config.SSHRetryInitialDelay
	var lastErr error
	var tunnelInfo *TunnelInfo

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logger.Debug("Retrying SSH tunnel establishment (attempt %d/%d) after %v", attempt+1, maxRetries, retryDelay)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled while retrying tunnel establishment: %w", ctx.Err())
			case <-time.After(retryDelay):
				retryDelay *= 2 // Exponential backoff
				if retryDelay > config.SSHRetryMaxDelay {
					retryDelay = config.SSHRetryMaxDelay
				}
			}
		}

		// Find a free local port automatically (do this in the loop in case previous port became unavailable)
		localPort, err := findFreePort()
		if err != nil {
			lastErr = fmt.Errorf("failed to find free local port: %w", err)
			continue
		}
		localPortStr := strconv.Itoa(localPort)

		// Start the SSH tunnel with context
		// --== NOTE! If sshClient was created with NewClientWithJumpHost, it already handles jump host routing ==--
		stopFunc, err := sshClient.StartTunnel(ctx, localPortStr, remotePort)
		if err != nil {
			lastErr = fmt.Errorf("failed to start SSH tunnel (local:%d -> remote:%d): %w", localPort, remotePortInt, err)
			logger.Warn("SSH tunnel establishment failed (attempt %d/%d): %v", attempt+1, maxRetries, lastErr)
			continue
		}

		tunnelInfo = &TunnelInfo{
			LocalPort:  localPort,
			RemotePort: remotePortInt,
			StopFunc:   stopFunc,
		}
		logger.Info("SSH tunnel established: local:%d -> remote:%d", localPort, remotePortInt)
		return tunnelInfo, nil
	}

	return nil, fmt.Errorf("failed to establish SSH tunnel after %d attempts: %w", maxRetries, lastErr)
}

// findFreePort finds an available TCP port on localhost
func findFreePort() (int, error) {
	listener, err := netstd.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	addr := listener.Addr().(*netstd.TCPAddr)
	return addr.Port, nil
}
