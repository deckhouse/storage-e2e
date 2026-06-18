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
	"log/slog"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// defaultDialTimeout bounds a single (re)connect attempt performed by the
// connection core. It is deliberately internal: callers shape overall
// patience through context deadlines and WithRetries, while this only caps one
// detached dial so a reconnect storm can never hang indefinitely.
const defaultDialTimeout = 30 * time.Second

// options holds the resolved configuration for a Client. The zero value is not
// used directly; defaultOptions seeds sensible defaults that individual Option
// funcs then override.
type options struct {
	keepalive   time.Duration
	retries     int
	log         *slog.Logger
	hostKey     ssh.HostKeyCallback
	dialTimeout time.Duration
}

// defaultOptions returns the baseline configuration. Host key verification
// defaults to InsecureIgnoreHostKey because this package targets ephemeral e2e
// VMs whose host keys are not known ahead of time; this is a conscious default
// that WithHostKeyCallback overrides.
func defaultOptions() options {
	return options{
		keepalive: 0,
		retries:   config.SSHRetryCount,
		log:       logger.GetLogger(),
		//nolint:gosec // G106: ephemeral e2e VMs have no known host key; conscious default, overridable via WithHostKeyCallback.
		hostKey:     ssh.InsecureIgnoreHostKey(),
		dialTimeout: defaultDialTimeout,
	}
}

// Option configures a Client. Options are applied in order; later options win.
type Option func(*options)

// WithKeepalive enables a background keepalive probe at interval d. A non-zero
// interval starts a goroutine that sends "keepalive@openssh.com" and proactively
// heals the connection on failure. The zero value (default) disables keepalive.
func WithKeepalive(d time.Duration) Option {
	return func(o *options) { o.keepalive = d }
}

// WithRetries sets how many times an operation re-establishes the connection
// before giving up. Negative values are clamped to zero (no reconnect retries).
func WithRetries(n int) Option {
	return func(o *options) {
		if n < 0 {
			n = 0
		}
		o.retries = n
	}
}

// WithLogger sets the structured logger used for healing WARN messages and
// diagnostics. A nil logger is ignored so the default logger remains in place.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.log = l
		}
	}
}

// WithHostKeyCallback sets the host key verification callback used for every hop
// that does not carry its own Endpoint.HostKey. A nil callback is ignored.
func WithHostKeyCallback(cb ssh.HostKeyCallback) Option {
	return func(o *options) {
		if cb != nil {
			o.hostKey = cb
		}
	}
}

// WithInsecureIgnoreHostKey disables host key verification for hops without an
// explicit Endpoint.HostKey. This is the default, but the option exists so the
// intent can be made explicit at the call site.
func WithInsecureIgnoreHostKey() Option {
	//nolint:gosec // G106: explicit opt-in to skip host key verification.
	return func(o *options) { o.hostKey = ssh.InsecureIgnoreHostKey() }
}
