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

type conn struct {
	dialer      Dialer
	log         *slog.Logger
	dialTimeout time.Duration

	flight singleflight.Group

	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	mu     sync.Mutex
	client *ssh.Client
	closer io.Closer
	gen    uint64
	closed bool

	wg sync.WaitGroup
}

func newConn(ctx context.Context, d Dialer, o options) (*conn, error) {
	client, closer, err := d.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", d.Describe(), err)
	}

	lifeCtx, lifeCancel := context.WithCancel(context.WithoutCancel(ctx))

	c := &conn{
		dialer:      d,
		log:         o.log,
		dialTimeout: o.dialTimeout,
		client:      client,
		closer:      closer,
		gen:         1,
		lifeCtx:     lifeCtx,
		lifeCancel:  lifeCancel,
	}

	if o.keepalive > 0 {
		probeTimeout := resolveKeepaliveTimeout(o.keepalive, o.keepaliveTimeout)
		c.wg.Add(1)
		go c.keepaliveLoop(lifeCtx, o.keepalive, probeTimeout)
	}

	return c, nil
}

func (c *conn) snapshot() (client *ssh.Client, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client, c.gen
}

func (c *conn) refresh(failedGen uint64) (*ssh.Client, uint64, error) {
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
		if c.gen != failedGen {
			cur := healed{client: c.client, gen: c.gen}
			c.mu.Unlock()
			return cur, nil
		}
		c.mu.Unlock()

		// Reconnect dials run on the connection-lifetime context, not on any
		// per-caller ctx: this keeps the singleflight-shared dial isolated from
		// one caller's cancellation while still letting Close() abort it.
		dialCtx, cancel := context.WithTimeout(c.lifeCtx, c.dialTimeout)
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

		if old != nil {
			_ = old.Close()
		}
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

func (c *conn) keepaliveLoop(ctx context.Context, interval, probeTimeout time.Duration) {
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
			if err := probeKeepalive(ctx, client, probeTimeout); err == nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			c.log.Warn("ssh: keepalive failed, healing connection",
				"route", c.dialer.Describe())
			if _, _, err := c.refresh(gen); err != nil {
				if c.isClosed() || ctx.Err() != nil {
					return
				}
				c.log.Warn("ssh: keepalive-triggered reconnect failed",
					"route", c.dialer.Describe(), "err", err)
			}
		}
	}
}

func probeKeepalive(ctx context.Context, client *ssh.Client, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		errc <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("ssh: keepalive probe timed out after %s", timeout)
	case err := <-errc:
		return err
	}
}

func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	closer := c.closer
	c.client = nil
	c.closer = nil
	c.mu.Unlock()

	c.lifeCancel()
	c.wg.Wait()

	if closer != nil {
		if err := closer.Close(); err != nil && !isTransient(err) {
			return err
		}
	}
	return nil
}

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

		client, gen, err = c.refresh(gen)
		if err != nil {
			return zero, fmt.Errorf("heal connection: %w", err)
		}
	}
}
