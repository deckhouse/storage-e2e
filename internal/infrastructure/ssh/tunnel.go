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
	"time"

	"golang.org/x/crypto/ssh"
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

// EstablishSSHTunnel establishes an SSH tunnel with port forwarding from remote node to local port on the client
// It uses the exact port specified in remotePort and fails immediately if the port is busy
// Returns the tunnel info, local port and error if the tunnel fails to start
func EstablishSSHTunnel(ctx context.Context, sshClient SSHClient, remotePort string) (*TunnelInfo, error) {
	// Parse remote port to integer
	remotePortInt, err := strconv.Atoi(remotePort)
	if err != nil {
		return nil, fmt.Errorf("invalid remote port %s: %w", remotePort, err)
	}

	// Start the SSH tunnel with context
	// --== NOTE! If sshClient was created with NewClientWithJumpHost, it already handles jump host routing ==--
	stopFunc, err := sshClient.StartTunnel(ctx, remotePort, remotePort)
	if err != nil {
		return nil, fmt.Errorf("failed to start SSH tunnel on port %d (port may be busy): %w", remotePortInt, err)
	}

	tunnelInfo := &TunnelInfo{
		LocalPort:  remotePortInt,
		RemotePort: remotePortInt,
		StopFunc:   stopFunc,
	}

	return tunnelInfo, nil
}
