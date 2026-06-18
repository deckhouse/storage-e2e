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
)

// errClosed is returned by the connection core once Close has been called.
var errClosed = errors.New("ssh: client is closed")

// isTransient reports whether err denotes a recoverable transport failure that
// healing the SSH connection might fix (a dropped session, a reset peer, a
// timed-out read, …). Classification is done structurally via errors.Is and
// errors.As against standard error values and types — never by matching error
// text — so it stays correct as wrapping changes.
//
// Context cancellation (context.Canceled, context.DeadlineExceeded) is
// deliberately NOT transient: those mean the caller asked to stop, so retrying
// would ignore an explicit signal.
func isTransient(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation outranks everything: it is an explicit stop signal,
	// not a recoverable transport failure. Check it first because
	// context.DeadlineExceeded also satisfies net.Error with Timeout()==true.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// A clean or truncated EOF is the most common symptom of a session that
	// died underneath us (the x/crypto/ssh mux surfaces the stored disconnect
	// error, usually io.EOF, to pending channel/session opens).
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Operating on a connection/listener that was already closed by a peer or
	// by our own reconnect.
	if errors.Is(err, net.ErrClosed) {
		return true
	}

	// Low-level socket failures that a fresh dial typically recovers from.
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	// Any net.Error that reports a timeout (covers i/o timeouts that are not a
	// bare syscall.ETIMEDOUT, e.g. deadline-driven failures).
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}

	return false
}

// ExitError reports that a remote command ran to completion but exited with a
// non-zero status. It is intentionally distinct from a transport error: a
// non-zero exit is a normal program outcome, not a broken connection, so the
// operation core must never retry it.
//
// It is part of the contract for the future Run operation (see package docs);
// the connection core already treats *ExitError as non-transient because
// isTransient returns false for it.
type ExitError struct {
	// Cmd is the command line that was executed.
	Cmd string
	// ExitCode is the process exit status reported by the remote end.
	ExitCode int
	// Stderr holds captured standard error, when available.
	Stderr string
	// Err is the underlying error returned by the SSH library, if any.
	Err error
}

// Error implements the error interface.
func (e *ExitError) Error() string {
	return fmt.Sprintf("ssh: command %q exited with code %d", e.Cmd, e.ExitCode)
}

// Unwrap exposes the underlying SSH library error for errors.Is/As.
func (e *ExitError) Unwrap() error { return e.Err }
