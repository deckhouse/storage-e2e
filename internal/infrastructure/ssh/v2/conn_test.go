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
	"io"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func newTestConn(t *testing.T, d Dialer, keepalive time.Duration) *conn {
	t.Helper()
	o := defaultOptions()
	o.log = quietLogger()
	o.keepalive = keepalive
	o.dialTimeout = 5 * time.Second
	c, err := newConn(context.Background(), d, o)
	if err != nil {
		t.Fatalf("newConn: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestConnSnapshotInitialGeneration(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}

	c := newTestConn(t, d, 0)
	client, gen := c.snapshot()
	if client == nil {
		t.Fatalf("snapshot returned nil client")
	}
	if gen != 1 {
		t.Fatalf("initial generation = %d, want 1", gen)
	}
	if d.dialCount() != 1 {
		t.Fatalf("dial count = %d, want 1", d.dialCount())
	}
}

func TestConnRefreshStaleGenerationDoesNotReconnect(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	client, gen, err := c.refresh(context.Background(), 0)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if gen != 1 {
		t.Fatalf("generation = %d, want 1 (unchanged)", gen)
	}
	if client == nil {
		t.Fatalf("refresh returned nil client")
	}
	if d.dialCount() != 1 {
		t.Fatalf("dial count = %d, want 1 (no reconnect)", d.dialCount())
	}
}

func TestConnRefreshDeduplicatesConcurrentReconnects(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	if d.dialCount() != 1 {
		t.Fatalf("setup dial count = %d, want 1", d.dialCount())
	}

	gate := make(chan struct{})
	d.setGate(gate)

	const n = 8
	var wg sync.WaitGroup
	gens := make([]uint64, n)
	errs := make([]error, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, gen, err := c.refresh(context.Background(), 1)
			gens[i] = gen
			errs[i] = err
		}(i)
	}

	close(start)
	waitFor(t, 2*time.Second, func() bool { return d.dialCount() == 2 })
	close(gate)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("refresher %d error: %v", i, errs[i])
		}
		if gens[i] != 2 {
			t.Fatalf("refresher %d generation = %d, want 2", i, gens[i])
		}
	}
	if d.dialCount() != 2 {
		t.Fatalf("dial count = %d, want 2 (one reconnect for all callers)", d.dialCount())
	}
}

func TestWithConnHealsOnTransientFailure(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	var calls int
	got, err := withConn(context.Background(), c, 3, func(_ context.Context, client *ssh.Client) (string, error) {
		calls++
		if calls == 1 {
			return "", io.EOF // looks like a dropped session
		}
		if client == nil {
			return "", errors.New("nil client after heal")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("withConn: %v", err)
	}
	if got != "ok" {
		t.Fatalf("result = %q, want ok", got)
	}
	if calls != 2 {
		t.Fatalf("op calls = %d, want 2", calls)
	}
	if d.dialCount() != 2 {
		t.Fatalf("dial count = %d, want 2 (one heal)", d.dialCount())
	}
}

func TestWithConnDoesNotRetryNonTransient(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	sentinel := errors.New("application error")
	var calls int
	_, err := withConn(context.Background(), c, 3, func(_ context.Context, _ *ssh.Client) (struct{}, error) {
		calls++
		return struct{}{}, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("op calls = %d, want 1 (no retry)", calls)
	}
	if d.dialCount() != 1 {
		t.Fatalf("dial count = %d, want 1 (no reconnect)", d.dialCount())
	}
}

func TestWithConnRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls int
	_, err := withConn(ctx, c, 3, func(_ context.Context, _ *ssh.Client) (struct{}, error) {
		calls++
		return struct{}{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("op calls = %d, want 0 (ctx already canceled)", calls)
	}
}

func TestWithConnExhaustsRetries(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	var calls int
	_, err := withConn(context.Background(), c, 2, func(_ context.Context, _ *ssh.Client) (struct{}, error) {
		calls++
		return struct{}{}, io.EOF
	})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want wrapped io.EOF", err)
	}
	if calls != 3 {
		t.Fatalf("op calls = %d, want 3", calls)
	}
}

func TestConnCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestConn(t, d, 0)

	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, _, err := c.refresh(context.Background(), 1); !errors.Is(err, errClosed) {
		t.Fatalf("refresh after close = %v, want errClosed", err)
	}
}

func TestKeepaliveHealsDroppedConnection(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	_ = newTestConn(t, d, 100*time.Millisecond)

	srv.dropConns()

	waitFor(t, 5*time.Second, func() bool { return d.dialCount() >= 2 })
	if d.dialCount() < 2 {
		t.Fatalf("dial count = %d, want >= 2 (keepalive heal)", d.dialCount())
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
