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
	"bytes"
	"context"
	"fmt"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Exec runs cmd on the remote host and returns the combined stdout+stderr.
// A non-zero remote exit status returns a non-nil error together with the
// captured output. The call heals and retries on transient connection
// failures using the same executor as OpenTunnel.
func (c *Client) Exec(ctx context.Context, cmd string) (string, error) {
	// withConn discards its result value whenever the op returns an error, so
	// the captured output is published through this closure variable instead.
	// That keeps the output available on a non-zero exit (a non-transient
	// *ssh.ExitError), where withConn returns the zero result alongside err.
	var output string
	_, err := withConn(ctx, c.conn, c.retries, func(ctx context.Context, client *ssh.Client) (struct{}, error) {
		session, err := client.NewSession()
		if err != nil {
			return struct{}{}, fmt.Errorf("open ssh session: %w", err)
		}
		defer session.Close()

		// ssh copies stdout and stderr from two separate goroutines, so the
		// shared sink must be synchronized to combine them safely.
		var buf combinedBuffer
		session.Stdout = &buf
		session.Stderr = &buf

		runErr := runWithContext(ctx, session, cmd)
		output = buf.String()
		return struct{}{}, runErr
	})
	return output, err
}

// combinedBuffer is a mutex-guarded buffer that serializes the concurrent
// stdout/stderr writes ssh performs so their interleaving is race-free.
type combinedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *combinedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *combinedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runWithContext runs cmd and aborts the session if ctx is cancelled.
func runWithContext(ctx context.Context, session *ssh.Session, cmd string) error {
	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- session.Wait() }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("command %q failed: %w", cmd, err)
		}
		return nil
	}
}
