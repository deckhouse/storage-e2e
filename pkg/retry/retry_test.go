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

package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// statusErr constructs a *apierrors.StatusError with the given HTTP code and
// optional RetryAfterSeconds, mirroring how apiserver decodes failures.
func statusErr(code int32, retryAfterSec int32) *apierrors.StatusError {
	st := metav1.Status{
		Status: metav1.StatusFailure,
		Code:   code,
	}
	if retryAfterSec > 0 {
		st.Details = &metav1.StatusDetails{RetryAfterSeconds: retryAfterSec}
	}
	return &apierrors.StatusError{ErrStatus: st}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},

		// Status code branch.
		{"status 500", statusErr(500, 0), true},
		{"status 503", statusErr(503, 0), true},
		{"status 429 (too many requests)", statusErr(429, 0), true},
		{"status 501 not implemented", statusErr(501, 0), false},
		{"status 200 ok", statusErr(200, 0), false},
		{"status 404 not found", statusErr(404, 0), false},
		{"status with RetryAfterSeconds hint", statusErr(400, 7), true},

		// Helper-based detection from apimachinery.
		{
			"server timeout via helper",
			apierrors.NewServerTimeout(schema.GroupResource{Resource: "pods"}, "x", 1),
			true,
		},
		{
			"service unavailable via helper",
			apierrors.NewServiceUnavailable("svc"),
			true,
		},
		{
			"too many requests via helper",
			apierrors.NewTooManyRequestsError("rl"),
			true,
		},
		{
			"internal error via helper",
			apierrors.NewInternalError(errors.New("boom")),
			true,
		},

		// io.EOF is always retryable (broken connections).
		{"io.EOF", io.EOF, true},
		{"wrapped io.EOF", fmt.Errorf("read tcp: %w", io.EOF), true},

		// String-pattern branches.
		{"TLS handshake timeout", errors.New("TLS handshake timeout"), true},
		{"connection refused", errors.New("dial: connection refused"), true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"connection timed out", errors.New("connection timed out"), true},
		{"i/o timeout", errors.New("read tcp: i/o timeout"), true},
		{"EOF substring", errors.New("transport: EOF"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"no route to host", errors.New("dial: no route to host"), true},
		{"network is unreachable", errors.New("dial: network is unreachable"), true},
		{"net/http: request canceled", errors.New("net/http: request canceled"), true},
		{"context deadline exceeded", errors.New("context deadline exceeded"), true},

		{"k8s server unable", errors.New("the server is currently unable to handle the request"), true},
		{"ServiceUnavailable msg", errors.New("ServiceUnavailable"), true},
		{"etcdserver timeout", errors.New("etcdserver: request timed out"), true},
		{"etcdserver leader changed", errors.New("etcdserver: leader changed"), true},
		{"failed to get server groups", errors.New("failed to get server groups"), true},

		{"ssh handshake failed", errors.New("ssh: handshake failed"), true},
		{"ssh unable to authenticate", errors.New("ssh: unable to authenticate"), true},
		{"ssh connection lost", errors.New("ssh: connection lost"), true},
		{"ssh failed to dial", errors.New("failed to dial 1.2.3.4:22"), true},
		{"closed network connection", errors.New("use of closed network connection"), true},

		{"webhook calling failure", errors.New("failed calling webhook validate.k8s.io"), true},
		{"plain webhook word", errors.New("webhook handler exploded"), true},

		// Non-retryable.
		{"plain generic error", errors.New("oops"), false},
		{"validation failure", errors.New("validation failed: invalid name"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRetryable(tc.err)
			if got != tc.want {
				t.Fatalf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsSSHConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},

		{"failed to create SSH session", errors.New("failed to create SSH session: foo"), true},
		{"ssh handshake failed", errors.New("ssh: handshake failed"), true},
		{"ssh connection lost", errors.New("ssh: connection lost"), true},
		{"closed network connection", errors.New("use of closed network connection"), true},
		{"connection refused", errors.New("connection refused"), true},
		{"connection reset", errors.New("connection reset"), true},
		{"broken pipe", errors.New("broken pipe"), true},
		{"EOF string", errors.New("io: EOF"), true},
		{"io timeout", errors.New("i/o timeout"), true},

		{"io.EOF via errors.Is", io.EOF, true},
		{"wrapped io.EOF", fmt.Errorf("wrap: %w", io.EOF), true},

		// Not a connection error.
		{"command not found", errors.New("bash: foo: command not found"), false},
		{"permission denied", errors.New("permission denied"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSSHConnectionError(tc.err)
			if got != tc.want {
				t.Fatalf("IsSSHConnectionError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithRetryAfter(t *testing.T) {
	base := Config{
		MaxRetries:  5,
		InitialWait: 1 * time.Second,
		MaxWait:     10 * time.Second,
		Backoff:     2.0,
	}

	t.Run("nil error returns config unchanged", func(t *testing.T) {
		got := WithRetryAfter(base, nil)
		if got.InitialWait != base.InitialWait {
			t.Errorf("InitialWait changed: got %v, want %v", got.InitialWait, base.InitialWait)
		}
	})

	t.Run("non-status error returns config unchanged", func(t *testing.T) {
		got := WithRetryAfter(base, errors.New("boom"))
		if got.InitialWait != base.InitialWait {
			t.Errorf("InitialWait changed unexpectedly: got %v", got.InitialWait)
		}
	})

	t.Run("status error without RetryAfter returns config unchanged", func(t *testing.T) {
		got := WithRetryAfter(base, statusErr(503, 0))
		if got.InitialWait != base.InitialWait {
			t.Errorf("InitialWait changed unexpectedly: got %v", got.InitialWait)
		}
	})

	t.Run("RetryAfter larger than InitialWait overrides it", func(t *testing.T) {
		got := WithRetryAfter(base, statusErr(429, 5))
		want := 5 * time.Second
		if got.InitialWait != want {
			t.Errorf("InitialWait = %v, want %v", got.InitialWait, want)
		}
		// Other fields preserved.
		if got.MaxRetries != base.MaxRetries || got.MaxWait != base.MaxWait || got.Backoff != base.Backoff {
			t.Errorf("non-InitialWait fields changed: %+v vs %+v", got, base)
		}
	})

	t.Run("RetryAfter smaller than InitialWait does not shrink it", func(t *testing.T) {
		cfg := base
		cfg.InitialWait = 10 * time.Second
		got := WithRetryAfter(cfg, statusErr(429, 2))
		if got.InitialWait != cfg.InitialWait {
			t.Errorf("InitialWait shrunk to %v, want %v", got.InitialWait, cfg.InitialWait)
		}
	})
}

// fastCfg keeps Do's wall-clock cost low while still exercising backoff.
func fastCfg(maxRetries int) Config {
	return Config{
		MaxRetries:  maxRetries,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     4 * time.Millisecond,
		Backoff:     2.0,
		LogRetries:  false,
	}
}

func TestDo_SuccessFirstAttempt(t *testing.T) {
	var calls int32
	got, err := Do(context.Background(), fastCfg(3), "op", func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls)
	}
}

func TestDo_NonRetryableErrorReturnsImmediately(t *testing.T) {
	var calls int32
	_, err := Do(context.Background(), fastCfg(5), "op", func() (struct{}, error) {
		atomic.AddInt32(&calls, 1)
		return struct{}{}, errors.New("validation failed: bad input")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 call (non-retryable), got %d", calls)
	}
}

func TestDo_SuccessAfterRetries(t *testing.T) {
	var calls int32
	target := int32(3) // succeed on the 3rd call.
	got, err := Do(context.Background(), fastCfg(5), "op", func() (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < target {
			return "", io.EOF // retryable
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
	if atomic.LoadInt32(&calls) != target {
		t.Errorf("got %d calls, want %d", calls, target)
	}
}

func TestDo_FailsAfterMaxRetriesExhausted(t *testing.T) {
	var calls int32
	_, err := Do(context.Background(), fastCfg(2), "op", func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, io.EOF
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	// MaxRetries=2 means up to 3 attempts total.
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("got %d calls, want 3", got)
	}
}

func TestDo_ContextCancelBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	var calls int32
	_, err := Do(ctx, fastCfg(5), "op", func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("fn must not be called when ctx already cancelled, got %d calls", calls)
	}
}

func TestDo_ContextCancelDuringWait(t *testing.T) {
	cfg := Config{
		MaxRetries:  10,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     1 * time.Second,
		Backoff:     2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	// Cancel shortly after the first failed attempt enters the wait.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := Do(ctx, cfg, "op", func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, io.EOF
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("got %d calls, want 1 (cancelled during first wait)", calls)
	}
	if elapsed >= cfg.InitialWait {
		t.Errorf("Do returned too late (%v >= InitialWait %v); cancel did not abort wait", elapsed, cfg.InitialWait)
	}
}

func TestDo_BackoffIsCapped(t *testing.T) {
	// With InitialWait=4ms, Backoff=10, MaxWait=8ms we would normally jump to
	// 40ms after the first retry — the cap must prevent that.
	cfg := Config{
		MaxRetries:  5,
		InitialWait: 4 * time.Millisecond,
		MaxWait:     8 * time.Millisecond,
		Backoff:     10.0,
	}

	var calls int32
	start := time.Now()
	_, err := Do(context.Background(), cfg, "op", func() (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, io.EOF
	})
	elapsed := time.Since(start)

	if !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
	// 5 waits, each capped at 8ms => upper bound ~40ms even though uncapped
	// backoff would explode to seconds. Allow ample slack for slow CI runners.
	if elapsed > 300*time.Millisecond {
		t.Errorf("elapsed %v suggests backoff was not capped (MaxWait=%v)", elapsed, cfg.MaxWait)
	}
	if atomic.LoadInt32(&calls) != 6 {
		t.Errorf("got %d calls, want 6 (MaxRetries+1)", calls)
	}
}

func TestDoVoid_DelegatesToDo(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var calls int32
		err := DoVoid(context.Background(), fastCfg(2), "op", func() error {
			atomic.AddInt32(&calls, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if atomic.LoadInt32(&calls) != 1 {
			t.Errorf("got %d calls, want 1", calls)
		}
	})

	t.Run("propagates non-retryable error", func(t *testing.T) {
		want := errors.New("bad input")
		var calls int32
		err := DoVoid(context.Background(), fastCfg(3), "op", func() error {
			atomic.AddInt32(&calls, 1)
			return want
		})
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
		if atomic.LoadInt32(&calls) != 1 {
			t.Errorf("got %d calls, want 1", calls)
		}
	})
}
