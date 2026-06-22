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

const defaultDialTimeout = 30 * time.Second

type options struct {
	keepalive   time.Duration
	retries     int
	log         *slog.Logger
	hostKey     ssh.HostKeyCallback
	dialTimeout time.Duration
}

func defaultOptions() options {
	return options{
		keepalive:   0,
		retries:     config.SSHRetryCount,
		log:         logger.GetLogger(),
		hostKey:     ssh.InsecureIgnoreHostKey(),
		dialTimeout: defaultDialTimeout,
	}
}

type Option func(*options)

func WithKeepalive(d time.Duration) Option {
	return func(o *options) { o.keepalive = d }
}

func WithRetries(n int) Option {
	return func(o *options) {
		if n < 0 {
			n = 0
		}
		o.retries = n
	}
}

func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.log = l
		}
	}
}

func WithHostKeyCallback(cb ssh.HostKeyCallback) Option {
	return func(o *options) {
		if cb != nil {
			o.hostKey = cb
		}
	}
}

func WithInsecureIgnoreHostKey() Option {
	//nolint:gosec // G106: explicit opt-in to skip host key verification.
	return func(o *options) { o.hostKey = ssh.InsecureIgnoreHostKey() }
}
