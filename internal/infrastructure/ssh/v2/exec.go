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
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

func (c *Client) Exec(ctx context.Context, cmd string) (ExecResult, error) {
	var res ExecResult
	_, err := withConn(ctx, c.conn, c.retries, func(ctx context.Context, client *ssh.Client) (struct{}, error) {
		session, err := client.NewSession()
		if err != nil {
			return struct{}{}, fmt.Errorf("open ssh session: %w", err)
		}
		defer session.Close()

		var stdout, stderr bytes.Buffer
		session.Stdout = &stdout
		session.Stderr = &stderr

		runErr := runWithContext(ctx, session, cmd)
		res.Stdout = stdout.Bytes()
		res.Stderr = stderr.Bytes()

		var exitErr *ssh.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
		}
		return struct{}{}, runErr
	})
	return res, err
}

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
