/*
Copyright 2026 Flant JSC

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
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// flakyDialer fails its first failN Dial calls with a transient-looking error,
// then delegates to inner. When inner is nil (failN effectively infinite) it
// always fails; desc backs Describe so no server is required.
type flakyDialer struct {
	inner Dialer
	desc  string

	mu    sync.Mutex
	calls int
	failN int
}

func (d *flakyDialer) Dial(ctx context.Context) (*ssh.Client, io.Closer, error) {
	d.mu.Lock()
	d.calls++
	fail := d.calls <= d.failN
	d.mu.Unlock()

	if fail {
		return nil, nil, errors.New("connection refused")
	}
	return d.inner.Dial(ctx)
}

func (d *flakyDialer) Describe() string {
	if d.desc != "" {
		return d.desc
	}
	return d.inner.Describe()
}

func (d *flakyDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestNewWithRetryConnectsAfterN(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &flakyDialer{inner: &serverDialer{addr: srv.addr()}, failN: 2}

	c, err := NewWithRetry(t.Context(), d, 10*time.Millisecond, 2*time.Second,
		WithLogger(quietLogger()))
	if err != nil {
		t.Fatalf("NewWithRetry: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := d.callCount(); got != 3 {
		t.Fatalf("dial calls = %d, want 3 (2 failures + 1 success)", got)
	}

	// The healed-in client must be a real, usable connection.
	res, err := c.Exec(t.Context(), "echo hi")
	if err != nil {
		t.Fatalf("Exec after retry connect: %v", err)
	}
	if string(res.Stdout) != "ok:echo hi" {
		t.Fatalf("Exec stdout = %q, want %q", res.Stdout, "ok:echo hi")
	}
}

func TestNewWithRetryImmediateSuccess(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	d := &flakyDialer{inner: &serverDialer{addr: srv.addr()}, failN: 0}

	c, err := NewWithRetry(t.Context(), d, time.Second, 2*time.Second,
		WithLogger(quietLogger()))
	if err != nil {
		t.Fatalf("NewWithRetry: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := d.callCount(); got != 1 {
		t.Fatalf("dial calls = %d, want 1 (immediate success)", got)
	}
}

func TestNewWithRetryTimeout(t *testing.T) {
	t.Parallel()
	d := &flakyDialer{desc: "test://always-fail", failN: 1 << 30}

	start := time.Now()
	c, err := NewWithRetry(t.Context(), d, 10*time.Millisecond, 60*time.Millisecond,
		WithLogger(quietLogger()))
	if err == nil {
		_ = c.Close()
		t.Fatal("NewWithRetry: expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("returned after %s, want >= timeout (60ms)", elapsed)
	}
	// The last dial error must be wrapped for diagnosis.
	if got := err.Error(); !strings.Contains(got, "connection refused") || !strings.Contains(got, "within") {
		t.Fatalf("error = %q, want it to wrap the last dial error and mention the timeout", got)
	}
	if d.callCount() < 2 {
		t.Fatalf("dial calls = %d, want >= 2 (retried before timeout)", d.callCount())
	}
}

func TestNewWithRetryContextCanceled(t *testing.T) {
	t.Parallel()
	d := &flakyDialer{desc: "test://always-fail", failN: 1 << 30}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c, err := NewWithRetry(ctx, d, 10*time.Millisecond, 5*time.Second,
		WithLogger(quietLogger()))
	if err == nil {
		_ = c.Close()
		t.Fatal("NewWithRetry: expected context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewWithRetryNilDialer(t *testing.T) {
	t.Parallel()
	if _, err := NewWithRetry(t.Context(), nil, time.Second, time.Second); err == nil {
		t.Fatal("NewWithRetry(nil): expected error, got nil")
	}
}
