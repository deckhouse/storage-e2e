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
	"fmt"
	"io"
	netstd "net"
	"strconv"

	netpkg "github.com/deckhouse/storage-e2e/internal/infrastructure/net"
	"golang.org/x/crypto/ssh"
)

// StartTunnel starts an SSH tunnel with port forwarding from local to remote
// It returns a function to stop the tunnel and an error if the tunnel fails to start
func StartTunnel(sshClient *ssh.Client, localPort, remotePort string) (func() error, error) {
	listener, err := netstd.Listen("tcp", "127.0.0.1:"+localPort)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on local port %s: %w", localPort, err)
	}

	stopChan := make(chan struct{})

	go func() {
		defer listener.Close()
		for {
			// Check if we should stop before accepting
			select {
			case <-stopChan:
				return
			default:
			}

			// Set a deadline for Accept to allow periodic checking of stopChan
			localConn, err := listener.Accept()
			if err != nil {
				// Listener closed or error occurred
				select {
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

				// Copy data bidirectionally
				done := make(chan struct{}, 2)
				go func() {
					io.Copy(localConn, remoteConn)
					done <- struct{}{}
				}()
				go func() {
					io.Copy(remoteConn, localConn)
					done <- struct{}{}
				}()

				// Wait for either direction to finish
				<-done
			}()
		}
	}()

	stop := func() error {
		close(stopChan)
		return listener.Close()
	}

	return stop, nil
}

// EstablishSSHTunnel establishes an SSH tunnel with port forwarding from the master node to the same port of client, running the test
// It finds a free local port starting from remotePort and creates the tunnel
// Returns the tunnel info, local port and error if the tunnel fails to start
func EstablishSSHTunnel(sshClient SSHClient, remotePort string) (*TunnelInfo, error) {
	// Find a free local port starting from remotePort
	remotePortInt := 1024
	if parsed, err := strconv.Atoi(remotePort); err == nil {
		remotePortInt = parsed
	}

	localPort, err := netpkg.FindFreePort(remotePortInt)
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}

	// Start the SSH tunnel
	stopFunc, err := sshClient.StartTunnel(strconv.Itoa(localPort), remotePort)
	if err != nil {
		return nil, fmt.Errorf("failed to start SSH tunnel: %w", err)
	}

	tunnelInfo := &TunnelInfo{
		LocalPort:  localPort,
		RemotePort: remotePortInt,
		StopFunc:   stopFunc,
	}

	return tunnelInfo, nil
}
