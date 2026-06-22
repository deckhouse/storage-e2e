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
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestIsTransient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "io.EOF", err: io.EOF, want: true},
		{name: "wrapped EOF", err: fmt.Errorf("dial: %w", io.EOF), want: true},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "net closed", err: net.ErrClosed, want: true},
		{name: "wrapped net closed", err: fmt.Errorf("accept: %w", net.ErrClosed), want: true},
		{name: "ECONNRESET", err: syscall.ECONNRESET, want: true},
		{name: "ECONNREFUSED", err: syscall.ECONNREFUSED, want: true},
		{name: "EPIPE", err: syscall.EPIPE, want: true},
		{name: "timeout net error", err: timeoutErr{}, want: true},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "exit error", err: &ExitError{Cmd: "false", ExitCode: 1}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTransient(tc.err); got != tc.want {
				t.Fatalf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestExitErrorUnwrap(t *testing.T) {
	t.Parallel()

	underlying := errors.New("session: exited")
	exit := &ExitError{Cmd: "do-thing", ExitCode: 2, Stderr: "nope", Err: underlying}

	if !errors.Is(exit, underlying) {
		t.Fatalf("errors.Is should find the wrapped error")
	}
	var target *ExitError
	if !errors.As(error(exit), &target) {
		t.Fatalf("errors.As should match *ExitError")
	}
	if target.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", target.ExitCode)
	}
}
