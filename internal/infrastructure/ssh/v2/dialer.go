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

type Dialer interface {
	Dial(ctx context.Context) (*ssh.Client, io.Closer, error)
	Describe() string
}

type hostKeyDefaulter interface {
	setDefaultHostKey(ssh.HostKeyCallback)
}

func Route(first Endpoint, more ...Endpoint) Dialer {
	hops := make([]Endpoint, 0, 1+len(more))
	hops = append(hops, first)
	hops = append(hops, more...)
	return &route{hops: hops}
}

type route struct {
	hops           []Endpoint
	defaultHostKey ssh.HostKeyCallback
}

func (r *route) setDefaultHostKey(cb ssh.HostKeyCallback) { r.defaultHostKey = cb }

func (r *route) Describe() string {
	labels := make([]string, len(r.hops))
	for i, hop := range r.hops {
		labels[i] = hop.label()
	}
	return strings.Join(labels, " -> ")
}

func (r *route) Dial(ctx context.Context) (cl *ssh.Client, closer io.Closer, err error) {
	chain := &chainCloser{}
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

func handshakeOver(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

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

type chainCloser struct {
	closers []io.Closer
}

func (cc *chainCloser) add(c io.Closer) {
	if c != nil {
		cc.closers = append(cc.closers, c)
	}
}

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
