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
	"net"
	"testing"
	"time"
)

func newTestClient(t *testing.T, d Dialer, keepalive time.Duration) *Client {
	t.Helper()
	c, err := New(context.Background(), d,
		WithLogger(quietLogger()),
		WithKeepalive(keepalive),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func dialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func roundtrip(t *testing.T, addr, payload string) string {
	t.Helper()
	conn, err := dialTimeout(addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel %s: %v", addr, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := readFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func TestTunnelForwardsTraffic(t *testing.T) {
	t.Parallel()
	echoPort := newEchoServer(t)
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClient(t, d, 0)

	tun, err := c.Tunnel(context.Background(), echoPort)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	defer tun.Close()

	if tun.LocalPort == 0 {
		t.Fatalf("expected a non-zero local port")
	}
	if tun.RemotePort != echoPort {
		t.Fatalf("RemotePort = %d, want %d", tun.RemotePort, echoPort)
	}
	if got := tun.LocalAddr(); got == "" {
		t.Fatalf("LocalAddr empty")
	}

	if got := roundtrip(t, tun.LocalAddr(), "hello-tunnel"); got != "hello-tunnel" {
		t.Fatalf("echo = %q, want hello-tunnel", got)
	}
}

func TestTunnelHealsAfterDroppedSession(t *testing.T) {
	t.Parallel()
	echoPort := newEchoServer(t)
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClient(t, d, 0)

	tun, err := c.Tunnel(context.Background(), echoPort)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	defer tun.Close()

	if got := roundtrip(t, tun.LocalAddr(), "before"); got != "before" {
		t.Fatalf("echo before drop = %q, want before", got)
	}

	srv.dropConns()

	var lastErr error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := tryRoundtrip(tun.LocalAddr(), "after")
		if err == nil && got == "after" {
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("tunnel did not heal after dropped session: %v", lastErr)
	}
	if d.dialCount() < 2 {
		t.Fatalf("dial count = %d, want >= 2 (healed)", d.dialCount())
	}
}

func tryRoundtrip(addr, payload string) (string, error) {
	conn, err := dialTimeout(addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return "", err
	}
	buf := make([]byte, len(payload))
	if _, err := readFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func TestTunnelCloseIsIdempotentAndStopsListener(t *testing.T) {
	t.Parallel()
	echoPort := newEchoServer(t)
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClient(t, d, 0)

	tun, err := c.Tunnel(context.Background(), echoPort)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	addr := tun.LocalAddr()

	if err := tun.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tun.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		conn, err := dialTimeout(addr, 200*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		return false
	})
	if conn, err := dialTimeout(addr, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("listener still accepting after Close")
	}
}

func TestTunnelStopsWhenContextCancelled(t *testing.T) {
	t.Parallel()
	echoPort := newEchoServer(t)
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClient(t, d, 0)

	ctx, cancel := context.WithCancel(context.Background())
	tun, err := c.Tunnel(ctx, echoPort)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	defer tun.Close()

	addr := tun.LocalAddr()
	cancel()

	waitFor(t, 2*time.Second, func() bool {
		conn, err := dialTimeout(addr, 200*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		return false
	})
	if conn, err := dialTimeout(addr, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("listener still accepting after context cancel")
	}
}

func TestNewRejectsNilDialer(t *testing.T) {
	t.Parallel()
	_, err := New(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for nil dialer")
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c, err := New(context.Background(), d, WithLogger(quietLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
