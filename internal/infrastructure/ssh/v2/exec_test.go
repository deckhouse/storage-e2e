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
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestClientWithRetries(t *testing.T, d Dialer, retries int) *Client {
	t.Helper()
	c, err := New(context.Background(), d, WithRetries(retries), WithInsecureIgnoreHostKey(), WithLogger(quietLogger()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestExecReturnsStdoutAndStderr(t *testing.T) {
	srv := newTestServer(t)
	srv.setExecHandler(func(cmd string) (string, string, uint32) {
		return "out:" + cmd, "err-stream", 0
	})

	c := newTestClientWithRetries(t, &serverDialer{addr: srv.addr()}, 0)

	res, err := c.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
	if got := string(res.Stdout); !strings.Contains(got, "out:echo hi") {
		t.Errorf("Stdout = %q, want it to contain %q", got, "out:echo hi")
	}
	if got := string(res.Stderr); !strings.Contains(got, "err-stream") {
		t.Errorf("Stderr = %q, want it to contain %q", got, "err-stream")
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestExecNonZeroExitReturnsErrorWithOutput(t *testing.T) {
	srv := newTestServer(t)
	srv.setExecHandler(func(cmd string) (string, string, uint32) {
		return "boom-output", "", 7
	})

	c := newTestClientWithRetries(t, &serverDialer{addr: srv.addr()}, 0)

	res, err := c.Exec(context.Background(), "false")
	if err == nil {
		t.Fatal("Exec() error = nil, want non-nil for non-zero exit")
	}
	if got := string(res.Stdout); !strings.Contains(got, "boom-output") {
		t.Errorf("Stdout = %q, want it to contain %q", got, "boom-output")
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
}

func TestExecRetriesOnTransientFailure(t *testing.T) {
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClientWithRetries(t, d, 3)

	// Dropping the live connection races x/crypto/ssh tearing the dead transport
	// down, so the first attempt after the drop can surface a non-transient
	// channel-open error before the failure settles into a transient EOF; poll
	// until withConn observes the EOF and self-heals.
	srv.dropConns()

	var res ExecResult
	var err error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		res, err = c.Exec(context.Background(), "whoami")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Exec() did not heal after dropped connection: %v", err)
	}
	if got := string(res.Stdout); !strings.Contains(got, "whoami") {
		t.Errorf("Stdout = %q, want it to contain %q", got, "whoami")
	}
	if d.dialCount() < 2 {
		t.Errorf("dialCount = %d, want >= 2 (initial + reconnect)", d.dialCount())
	}
}

func TestExecReturnsContextErrorWhenAlreadyCancelled(t *testing.T) {
	srv := newTestServer(t)
	c := newTestClientWithRetries(t, &serverDialer{addr: srv.addr()}, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Exec(ctx, "sleep 1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Exec() error = %v, want context.Canceled", err)
	}
}

func TestExecCancelDuringRun(t *testing.T) {
	srv := newTestServer(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{})
	var once sync.Once
	srv.setExecHandler(func(cmd string) (string, string, uint32) {
		once.Do(func() { close(started) })
		<-release
		return "", "", 0
	})

	c := newTestClientWithRetries(t, &serverDialer{addr: srv.addr()}, 0)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Exec(ctx, "sleep")
		errCh <- err
	}()

	<-started
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Exec() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exec() did not return after context cancellation")
	}
}
