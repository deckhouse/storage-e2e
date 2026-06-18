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

// Package ssh provides a self-healing SSH client whose connection strategy
// ("how we connect" — directly or through jump hosts) is separated from the
// operations performed over it ("what we do" — currently tunneling).
//
// The injection point is the Dialer: Route builds one for a direct connection or
// an arbitrary chain of jump hosts. New opens a Client over a Dialer and hides
// every reconnect: callers invoke methods and never reason about reconnection.
// All operations funnel through a single reconnect-aware executor (withConn) over
// a shared connection core (conn), so future operations such as Run and Upload
// can be added without touching the healing logic.
//
// The primary use case is opening a tunnel to the API server of a closed
// Kubernetes cluster and pointing a kubeconfig at it:
//
//	c, _ := ssh.New(ctx, ssh.Route(jumpEp, targetEp))
//	defer c.Close()
//	t, _ := c.Tunnel(ctx, 6443)
//	defer t.Close()
//	rest := &rest.Config{Host: "https://" + t.LocalAddr()}
package ssh

import (
	"context"
	"errors"
	"log/slog"
)

// Client is a self-healing SSH client over a Dialer-provided connection. It is
// safe for concurrent use; reconnects are transparent to callers.
type Client struct {
	conn    *conn
	retries int
	log     *slog.Logger
}

// New connects immediately over d, starts keepalive when enabled, and returns a
// ready Client. The context bounds the initial connection. If d implements the
// internal host-key defaulter (as the built-in Route does), the resolved
// host-key option is pushed into it so per-hop Endpoint.HostKey values take
// precedence over the Client-level default.
func New(ctx context.Context, d Dialer, opts ...Option) (*Client, error) {
	if d == nil {
		return nil, errors.New("ssh: nil dialer")
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	if hkd, ok := d.(hostKeyDefaulter); ok {
		hkd.setDefaultHostKey(o.hostKey)
	}

	core, err := newConn(ctx, d, o)
	if err != nil {
		return nil, err
	}

	return &Client{conn: core, retries: o.retries, log: o.log}, nil
}

// Close tears down the connection and its whole chain and stops keepalive. It is
// idempotent and safe for concurrent use. Open tunnels keep their listeners; the
// caller should Close those separately.
func (c *Client) Close() error {
	return c.conn.Close()
}
