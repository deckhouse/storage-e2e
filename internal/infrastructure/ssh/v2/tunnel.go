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

const acceptDeadline = 500 * time.Millisecond

type Tunnel struct {
	LocalPort  int
	RemotePort int

	listener  net.Listener
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

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

func (t *Tunnel) LocalAddr() string {
	return "127.0.0.1:" + strconv.Itoa(t.LocalPort)
}

func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		t.closeErr = t.listener.Close()
		t.wg.Wait()
	})
	return t.closeErr
}

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
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				continue
			}
			return
		}

		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.handle(ctx, core, retries, local, log)
		}()
	}
}

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

	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}

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
