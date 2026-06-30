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

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type testServer struct {
	ln        net.Listener
	cfg       *ssh.ServerConfig
	wg        sync.WaitGroup
	closeOnce sync.Once

	execHandler func(cmd string) (stdout, stderr string, exitStatus uint32)

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
	s.execHandler = func(cmd string) (string, string, uint32) {
		return "ok:" + cmd, "", 0
	}
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
		switch newCh.ChannelType() {
		case "direct-tcpip":
			go handleDirectTCPIP(newCh)
		case "session":
			go s.handleSession(newCh)
		default:
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

type execMsg struct{ Command string }

func (s *testServer) handleSession(newCh ssh.NewChannel) {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return
	}
	defer ch.Close()

	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		var m execMsg
		if err := ssh.Unmarshal(req.Payload, &m); err != nil {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			return
		}
		if req.WantReply {
			_ = req.Reply(true, nil)
		}
		stdout, stderr, status := s.execHandler(m.Command)
		_, _ = io.WriteString(ch, stdout)
		_, _ = ch.Stderr().Write([]byte(stderr))
		statusPayload := ssh.Marshal(struct{ Status uint32 }{status})
		_, _ = ch.SendRequest("exit-status", false, statusPayload)
		return
	}
}

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
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
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
