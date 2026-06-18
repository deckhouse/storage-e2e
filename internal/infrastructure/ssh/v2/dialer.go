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
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Dialer is the injection point that decides how a live connection to the
// target host is established. Implementations hide whether the path is direct
// or routed through one or more jump hosts; the rest of the package only sees a
// ready *ssh.Client plus a Closer for the whole chain.
type Dialer interface {
	// Dial brings up a live connection to the target host, transparently
	// traversing any intermediate jump hops. The returned io.Closer tears down
	// the ENTIRE chain (target + every jump + any ssh-agent connection). It
	// must honor ctx for cancellation and deadlines.
	Dial(ctx context.Context) (*ssh.Client, io.Closer, error)
	// Describe returns a human-readable description of the route for logs and
	// error messages.
	Describe() string
}

// hostKeyDefaulter lets the Client push its host-key default into a Dialer that
// supports per-hop host-key resolution (the built-in route). It is unexported
// on purpose: third-party Dialers simply ignore the Client host-key options and
// own their verification policy entirely.
type hostKeyDefaulter interface {
	setDefaultHostKey(ssh.HostKeyCallback)
}

// Route builds a Dialer for a path of one or more hops. first is the entry
// point; more lists subsequent hops in travel order, and the LAST element is
// always the target host. A single argument means a direct connection, two
// means one jump, and so on. The (first, more...) signature guarantees at least
// one hop at compile time.
func Route(first Endpoint, more ...Endpoint) Dialer {
	hops := make([]Endpoint, 0, 1+len(more))
	hops = append(hops, first)
	hops = append(hops, more...)
	return &route{hops: hops}
}

// route is the built-in Dialer implementation produced by Route.
type route struct {
	hops           []Endpoint
	defaultHostKey ssh.HostKeyCallback
}

// setDefaultHostKey implements hostKeyDefaulter.
func (r *route) setDefaultHostKey(cb ssh.HostKeyCallback) { r.defaultHostKey = cb }

// Describe renders the route as "user@host -> user@host -> ...".
func (r *route) Describe() string {
	labels := make([]string, len(r.hops))
	for i, hop := range r.hops {
		labels[i] = hop.label()
	}
	return strings.Join(labels, " -> ")
}

// Dial establishes the full chain: it dials the first hop over TCP, then for
// every subsequent hop opens a forwarded connection from the previous hop and
// performs a fresh SSH handshake on top of it. On any failure every resource
// opened so far is closed before returning.
func (r *route) Dial(ctx context.Context) (cl *ssh.Client, closer io.Closer, err error) {
	chain := &chainCloser{}
	// Unwind everything on error so a partially-built chain never leaks.
	defer func() {
		if err != nil {
			_ = chain.Close()
		}
	}()

	first := r.hops[0]
	cfg, agentCloser, cfgErr := first.clientConfig(ctx, r.defaultHostKey)
	if cfgErr != nil {
		return nil, nil, fmt.Errorf("build config for %s: %w", first.label(), cfgErr)
	}
	chain.add(agentCloser)

	current, dialErr := dialSSH(ctx, first.addr(), cfg)
	if dialErr != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", first.label(), dialErr)
	}
	chain.add(current)

	for _, hop := range r.hops[1:] {
		hopCfg, hopAgentCloser, hopErr := hop.clientConfig(ctx, r.defaultHostKey)
		if hopErr != nil {
			return nil, nil, fmt.Errorf("build config for %s: %w", hop.label(), hopErr)
		}
		chain.add(hopAgentCloser)

		next, jumpErr := dialThroughJump(ctx, current, hop.addr())
		if jumpErr != nil {
			return nil, nil, fmt.Errorf("dial %s via %s: %w", hop.label(), first.label(), jumpErr)
		}

		hopClient, handshakeErr := handshakeOver(ctx, next, hop.addr(), hopCfg)
		if handshakeErr != nil {
			_ = next.Close()
			return nil, nil, fmt.Errorf("handshake to %s: %w", hop.label(), handshakeErr)
		}
		chain.add(hopClient)
		current = hopClient
	}

	return current, chain, nil
}

// dialSSH performs a context-aware TCP dial followed by an SSH handshake. The
// context bounds the TCP connect, and its deadline (if any) bounds the
// handshake; the deadline is cleared once the handshake succeeds.
func dialSSH(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	client, err := handshakeOver(ctx, conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// handshakeOver runs the SSH client handshake on an existing net.Conn, honoring
// the context deadline during the handshake.
func handshakeOver(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return nil, err
	}
	// Clear the handshake deadline so it does not bleed into later traffic.
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// dialThroughJump opens a forwarded TCP connection to addr from the jump client
// while respecting ctx. ssh.Client.Dial has no context variant, so the dial runs
// in a goroutine and ctx cancellation abandons (and later closes) the result
// without leaking the goroutine.
func dialThroughJump(ctx context.Context, jump *ssh.Client, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := jump.Dial("tcp", addr)
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

// chainCloser closes a set of resources in reverse order of registration, so
// the target host is torn down before the jump hops that carry it. nil entries
// are ignored, letting callers register optional ssh-agent closers
// unconditionally.
type chainCloser struct {
	closers []io.Closer
}

// add registers c for later closing. A nil closer is ignored.
func (cc *chainCloser) add(c io.Closer) {
	if c != nil {
		cc.closers = append(cc.closers, c)
	}
}

// Close closes every registered resource in reverse order and joins any errors.
func (cc *chainCloser) Close() error {
	var errs []error
	for i := len(cc.closers) - 1; i >= 0; i-- {
		if err := cc.closers[i].Close(); err != nil && !isTransient(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("close ssh chain: %w", errors.Join(errs...))
}
