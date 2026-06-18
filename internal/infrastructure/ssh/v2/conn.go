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
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/singleflight"
)

// conn is the connection core shared by every high-level operation. It owns the
// current *ssh.Client together with the Closer for its whole chain and a
// monotonically increasing generation counter. All reconnect logic lives here so
// callers (Tunnel today, Run/Upload later) never see a reconnect: they ask for
// the live client, run their operation, and on a transient failure call withConn
// which heals the connection underneath them.
type conn struct {
	dialer      Dialer
	log         *slog.Logger
	dialTimeout time.Duration

	// flight deduplicates concurrent reconnects keyed by the failed generation,
	// preventing a reconnect storm from tearing down a freshly healed link.
	flight singleflight.Group

	mu     sync.Mutex
	client *ssh.Client
	closer io.Closer
	gen    uint64
	closed bool

	// keepalive lifecycle.
	kaCancel context.CancelFunc
	wg       sync.WaitGroup
}

// newConn establishes the initial connection and, when keepalive > 0, starts the
// background keepalive goroutine. The initial dial uses the caller's context so
// startup honors their deadline and cancellation.
func newConn(ctx context.Context, d Dialer, o options) (*conn, error) {
	client, closer, err := d.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", d.Describe(), err)
	}

	c := &conn{
		dialer:      d,
		log:         o.log,
		dialTimeout: o.dialTimeout,
		client:      client,
		closer:      closer,
		gen:         1,
	}

	if o.keepalive > 0 {
		// Keepalive must outlive the caller's setup context: the connection
		// stays alive until Close, not until the New call returns. A fresh root
		// context canceled by Close is therefore correct here.
		kaCtx, cancel := context.WithCancel(context.Background())
		c.kaCancel = cancel
		c.wg.Add(1)
		//nolint:contextcheck // intentional: keepalive lifetime is bound to Close, not the setup context.
		go c.keepaliveLoop(kaCtx, o.keepalive)
	}

	return c, nil
}

// snapshot returns the current client and its generation under the lock. The
// generation lets callers tell refresh which connection failed them, so a
// concurrent heal is not duplicated.
func (c *conn) snapshot() (client *ssh.Client, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client, c.gen
}

// refresh re-establishes the connection that failed at generation failedGen and
// returns the now-current client and generation. Concurrent callers that failed
// on the same generation are collapsed into a single dial via singleflight; a
// caller whose failedGen is already stale (someone else healed first) gets the
// current client back without dialing. The actual Dial runs outside the lock and
// on a detached context with its own timeout, so one caller's cancellation can
// never abort the shared reconnect that others are waiting on.
func (c *conn) refresh(ctx context.Context, failedGen uint64) (*ssh.Client, uint64, error) {
	key := strconv.FormatUint(failedGen, 10)

	type healed struct {
		client *ssh.Client
		gen    uint64
	}

	v, err, _ := c.flight.Do(key, func() (interface{}, error) {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errClosed
		}
		// Someone already healed past failedGen — reuse the live client.
		if c.gen != failedGen {
			cur := healed{client: c.client, gen: c.gen}
			c.mu.Unlock()
			return cur, nil
		}
		c.mu.Unlock()

		// Detach from the caller's context so one cancellation does not abort the
		// shared flight, but still bound the dial with our own timeout.
		dialCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.dialTimeout)
		defer cancel()

		client, closer, dialErr := c.dialer.Dial(dialCtx)
		if dialErr != nil {
			return nil, fmt.Errorf("reconnect to %s: %w", c.dialer.Describe(), dialErr)
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = closer.Close()
			return nil, errClosed
		}
		old := c.closer
		c.client = client
		c.closer = closer
		c.gen++
		newGen := c.gen
		c.mu.Unlock()

		// Tear down the dead chain outside the lock.
		if old != nil {
			_ = old.Close()
		}
		// Self-healing must be loud, not silent.
		c.log.Warn("ssh: connection re-established",
			"route", c.dialer.Describe(), "generation", newGen)

		return healed{client: client, gen: newGen}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	r, ok := v.(healed)
	if !ok {
		return nil, 0, fmt.Errorf("ssh: unexpected refresh result type %T", v)
	}
	return r.client, r.gen, nil
}

// keepaliveLoop periodically probes the connection. A failed probe is not just a
// reason to exit: it routes through refresh so the link is proactively healed via
// the same single path as a failed operation. Keepalive only narrows the window
// in which a dead connection is noticed; the authoritative "heal now" signal is
// still a failed operation.
func (c *conn) keepaliveLoop(ctx context.Context, interval time.Duration) {
	defer c.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			client, gen := c.snapshot()
			if client == nil {
				continue
			}
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
				continue
			}
			c.log.Warn("ssh: keepalive failed, healing connection",
				"route", c.dialer.Describe())
			if _, _, err := c.refresh(ctx, gen); err != nil {
				if c.isClosed() || ctx.Err() != nil {
					return
				}
				c.log.Warn("ssh: keepalive-triggered reconnect failed",
					"route", c.dialer.Describe(), "err", err)
			}
		}
	}
}

// isClosed reports whether Close has been called.
func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close tears down the connection and its whole chain and stops the keepalive
// goroutine. It is idempotent and safe for concurrent use.
func (c *conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	closer := c.closer
	cancel := c.kaCancel
	c.client = nil
	c.closer = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()

	if closer != nil {
		if err := closer.Close(); err != nil && !isTransient(err) {
			return err
		}
	}
	return nil
}

// withConn runs op against the live client and heals the connection on transient
// failures, retrying up to retries times. It is the single reconnect-aware
// executor that every high-level operation builds on, so the reconnect policy
// lives in exactly one place. op MUST be safe to invoke more than once; callers
// whose operation is not idempotent (e.g. a command that already started running)
// must classify their own mid-flight failures as non-transient before they reach
// here.
//
// It is a generic free function rather than a method because Go methods cannot
// have type parameters; T lets callers return a typed result (a net.Conn for a
// tunnel dial, a session for Run, …) without boxing.
func withConn[T any](ctx context.Context, c *conn, retries int, op func(context.Context, *ssh.Client) (T, error)) (T, error) {
	var zero T

	client, gen := c.snapshot()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if client == nil {
			return zero, errClosed
		}

		result, err := op(ctx, client)
		if err == nil {
			return result, nil
		}

		// An explicit cancellation outranks any transient classification.
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if !isTransient(err) {
			return zero, err
		}
		if attempt >= retries {
			return zero, fmt.Errorf("after %d attempt(s): %w", attempt+1, err)
		}

		c.log.Warn("ssh: operation failed on broken connection, healing",
			"route", c.dialer.Describe(), "attempt", attempt+1, "err", err)

		client, gen, err = c.refresh(ctx, gen)
		if err != nil {
			return zero, fmt.Errorf("heal connection: %w", err)
		}
	}
}
