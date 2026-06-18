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
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// acceptDeadline bounds each listener Accept so the serve loop re-checks its
// context promptly even when no client is connecting.
const acceptDeadline = 500 * time.Millisecond

// Tunnel is a local TCP forward to a port on the target host. It listens on
// 127.0.0.1 on an automatically chosen free port and heals transparently: when
// the SSH session drops, the next forwarded connection re-opens it via the
// connection core and the listener keeps serving instead of dying.
type Tunnel struct {
	// LocalPort is the chosen local port on 127.0.0.1.
	LocalPort int
	// RemotePort is the forwarded port on the target host.
	RemotePort int

	listener  net.Listener
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// Tunnel forwards remotePort on the target host to a fresh local port on
// 127.0.0.1 and returns once the listener is up. The returned Tunnel serves
// until its Close is called or ctx is canceled. Establishing each forwarded
// connection is reconnect-aware and bounded by the Client's retry budget; every
// heal is logged at WARN.
func (c *Client) Tunnel(ctx context.Context, remotePort int) (*Tunnel, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("tunnel setup: %w", err)
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on local port: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("unexpected listener address type %T", listener.Addr())
	}
	localPort := tcpAddr.Port

	// The serve loop outlives the setup call, so derive a cancellable context
	// from the caller's: caller cancellation stops the tunnel, and so does Close.
	serveCtx, cancel := context.WithCancel(ctx)

	t := &Tunnel{
		LocalPort:  localPort,
		RemotePort: remotePort,
		listener:   listener,
		cancel:     cancel,
	}

	t.wg.Add(1)
	go t.serve(serveCtx, c.conn, c.retries, c.log)

	c.log.Info("ssh: tunnel established",
		"local", t.LocalAddr(), "remote_port", remotePort, "route", c.conn.dialer.Describe())

	return t, nil
}

// LocalAddr returns the local "127.0.0.1:<port>" address of the tunnel.
func (t *Tunnel) LocalAddr() string {
	return "127.0.0.1:" + strconv.Itoa(t.LocalPort)
}

// Close stops the tunnel: it cancels the serve loop, closes the listener, and
// waits for all in-flight connections to drain. It is idempotent and safe for
// concurrent use. It does not close the underlying SSH connection, which the
// owning Client manages.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		t.closeErr = t.listener.Close()
		t.wg.Wait()
	})
	return t.closeErr
}

// serve accepts local connections and forwards each one over the SSH connection.
// A short Accept deadline keeps the loop responsive to ctx; a dead session does
// not stop the loop — it is healed per connection in handle.
func (t *Tunnel) serve(ctx context.Context, core *conn, retries int, log *slog.Logger) {
	defer t.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if tcp, ok := t.listener.(*net.TCPListener); ok {
			_ = tcp.SetDeadline(time.Now().Add(acceptDeadline))
		}

		local, err := t.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// The listener was closed by Close (not via ctx); stop serving.
			return
		}

		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.handle(ctx, core, retries, local, log)
		}()
	}
}

// handle forwards a single accepted local connection to the remote port. The
// remote dial is reconnect-aware: a transient failure heals the SSH connection
// and retries within the budget. Once both ends are connected, bytes are copied
// in both directions; closing the conns on completion or cancellation unblocks
// any read still in flight.
func (t *Tunnel) handle(ctx context.Context, core *conn, retries int, local net.Conn, log *slog.Logger) {
	defer local.Close()

	remotePort := t.RemotePort
	remote, err := withConn(ctx, core, retries, func(ctx context.Context, client *ssh.Client) (net.Conn, error) {
		return dialChannel(ctx, client, "127.0.0.1:"+strconv.Itoa(remotePort))
	})
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("ssh: tunnel forward failed",
				"local", t.LocalAddr(), "remote_port", remotePort, "err", err)
		}
		return
	}
	defer remote.Close()

	// Closing both conns on cancellation unblocks the copy goroutines, which
	// would otherwise sit in a blocking Read.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = local.Close()
			_ = remote.Close()
		case <-stop:
		}
	}()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()

	// When one direction ends, close both ends to unblock the other.
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}

// dialChannel opens a forwarded TCP connection to addr over the SSH client while
// respecting ctx. ssh.Client.Dial has no context variant, so the dial runs in a
// goroutine and ctx cancellation abandons (and later closes) the result without
// leaking the goroutine.
func dialChannel(ctx context.Context, client *ssh.Client, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := client.Dial("tcp", addr)
		ch <- result{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		return r.conn, r.err
	}
}
