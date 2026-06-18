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
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// quietLogger returns a logger that discards output, keeping test logs clean.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testServer is an in-process SSH server on 127.0.0.1 used by tests. It accepts
// any client (NoClientAuth), answers keepalive global requests, and serves
// "direct-tcpip" channels by dialing the requested address and proxying bytes —
// enough to exercise tunnels end to end. dropConns force-closes live transports
// to simulate a dropped session.
type testServer struct {
	ln        net.Listener
	cfg       *ssh.ServerConfig
	wg        sync.WaitGroup
	closeOnce sync.Once

	mu    sync.Mutex
	conns []net.Conn
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatalf("build host signer: %v", err)
	}

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &testServer{ln: ln, cfg: cfg}
	s.wg.Add(1)
	go s.acceptLoop()
	t.Cleanup(s.Close)
	return s
}

func (s *testServer) addr() string { return s.ln.Addr().String() }

func (s *testServer) acceptLoop() {
	defer s.wg.Done()
	for {
		nConn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, nConn)
		s.mu.Unlock()

		s.wg.Add(1)
		go s.handleConn(nConn)
	}
}

func (s *testServer) handleConn(nConn net.Conn) {
	defer s.wg.Done()

	sconn, chans, reqs, err := ssh.NewServerConn(nConn, s.cfg)
	if err != nil {
		_ = nConn.Close()
		return
	}
	defer sconn.Close()

	go func() {
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		}
	}()

	for newCh := range chans {
		if newCh.ChannelType() != "direct-tcpip" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only direct-tcpip is supported")
			continue
		}
		go handleDirectTCPIP(newCh)
	}
}

// directTCPIPMsg is the extra data layout of a direct-tcpip channel open.
type directTCPIPMsg struct {
	DestAddr string
	DestPort uint32
	OrigAddr string
	OrigPort uint32
}

func handleDirectTCPIP(newCh ssh.NewChannel) {
	var msg directTCPIPMsg
	if err := ssh.Unmarshal(newCh.ExtraData(), &msg); err != nil {
		_ = newCh.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}

	target := net.JoinHostPort(msg.DestAddr, strconv.Itoa(int(msg.DestPort)))
	var dialer net.Dialer
	remote, err := dialer.DialContext(context.Background(), "tcp", target)
	if err != nil {
		_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
		return
	}

	ch, reqs, err := newCh.Accept()
	if err != nil {
		_ = remote.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	go func() {
		_, _ = io.Copy(ch, remote)
		_ = ch.Close()
	}()
	go func() {
		_, _ = io.Copy(remote, ch)
		_ = remote.Close()
	}()
}

// dropConns force-closes all live transport connections, simulating a session
// drop (a Wi-Fi flap on the developer's laptop).
func (s *testServer) dropConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
}

func (s *testServer) Close() {
	s.closeOnce.Do(func() {
		_ = s.ln.Close()
		s.dropConns()
		s.wg.Wait()
	})
}

// serverDialer is a test Dialer that connects to a testServer. It counts dials
// and can gate each dial on a channel to make reconnect concurrency
// deterministic.
type serverDialer struct {
	addr string

	mu    sync.Mutex
	dials int
	gate  chan struct{}
}

func (d *serverDialer) Dial(ctx context.Context) (*ssh.Client, io.Closer, error) {
	d.mu.Lock()
	d.dials++
	gate := d.gate
	d.mu.Unlock()

	if gate != nil {
		<-gate
	}

	client, err := dialSSH(ctx, d.addr, &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, nil, err
	}
	return client, client, nil
}

func (d *serverDialer) Describe() string { return "test://" + d.addr }

func (d *serverDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func (d *serverDialer) setGate(gate chan struct{}) {
	d.mu.Lock()
	d.gate = gate
	d.mu.Unlock()
}

// newEchoServer starts a TCP echo server on 127.0.0.1 and returns its port.
func newEchoServer(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port
}
