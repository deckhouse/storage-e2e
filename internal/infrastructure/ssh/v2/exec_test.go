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
	"strings"
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

func TestExecReturnsCombinedOutput(t *testing.T) {
	srv := newTestServer(t)
	srv.execHandler = func(cmd string) (string, string, uint32) {
		return "out:" + cmd, "err-stream", 0
	}

	c := newTestClient(t, &serverDialer{addr: srv.addr()}, 0)

	out, err := c.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
	if !strings.Contains(out, "out:echo hi") {
		t.Errorf("stdout missing from output: %q", out)
	}
	if !strings.Contains(out, "err-stream") {
		t.Errorf("stderr missing from combined output: %q", out)
	}
}

func TestExecNonZeroExitReturnsErrorWithOutput(t *testing.T) {
	srv := newTestServer(t)
	srv.execHandler = func(cmd string) (string, string, uint32) {
		return "boom-output", "", 7
	}

	c := newTestClient(t, &serverDialer{addr: srv.addr()}, 0)

	out, err := c.Exec(context.Background(), "false")
	if err == nil {
		t.Fatal("Exec() error = nil, want non-nil for non-zero exit")
	}
	if !strings.Contains(out, "boom-output") {
		t.Errorf("output not returned on failure: %q", out)
	}
}

func TestExecRetriesOnTransientFailure(t *testing.T) {
	srv := newTestServer(t)
	d := &serverDialer{addr: srv.addr()}
	c := newTestClientWithRetries(t, d, 3)

	// Drop the live connection so the next session attempt fails on a broken
	// transport and withConn heals + retries against the (still listening)
	// server. The very first NewSession after the drop can surface a
	// non-transient channel-open error while x/crypto/ssh tears the dead
	// transport down; once it observes the clean transport EOF the failure is
	// transient and withConn reconnects. Poll until the connection self-heals.
	srv.dropConns()

	var out string
	var err error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		out, err = c.Exec(context.Background(), "whoami")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Exec() did not heal after dropped connection: %v", err)
	}
	if !strings.Contains(out, "whoami") {
		t.Errorf("unexpected output after heal: %q", out)
	}
	if d.dialCount() < 2 {
		t.Errorf("dialCount = %d, want >= 2 (initial + reconnect)", d.dialCount())
	}
}

func TestExecRespectsContextCancellation(t *testing.T) {
	srv := newTestServer(t)
	c := newTestClient(t, &serverDialer{addr: srv.addr()}, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Exec(ctx, "sleep 1"); err == nil {
		t.Error("Exec() error = nil, want context error")
	}
	_ = time.Second
}
