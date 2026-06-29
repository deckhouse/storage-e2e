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
	"testing"
	"time"
)

func TestResolveKeepaliveTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		interval   time.Duration
		configured time.Duration
		want       time.Duration
	}{
		{
			name:       "explicit configured wins",
			interval:   30 * time.Second,
			configured: 3 * time.Second,
			want:       3 * time.Second,
		},
		{
			name:       "explicit may exceed interval",
			interval:   1 * time.Second,
			configured: 5 * time.Second,
			want:       5 * time.Second,
		},
		{
			name:     "unset caps at default for long interval",
			interval: 30 * time.Second,
			want:     defaultKeepaliveTimeout,
		},
		{
			name:     "unset uses interval when shorter than cap",
			interval: 2 * time.Second,
			want:     2 * time.Second,
		},
		{
			name:     "unset equal to cap uses cap",
			interval: defaultKeepaliveTimeout,
			want:     defaultKeepaliveTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveKeepaliveTimeout(tc.interval, tc.configured); got != tc.want {
				t.Fatalf("resolveKeepaliveTimeout(%s, %s) = %s, want %s",
					tc.interval, tc.configured, got, tc.want)
			}
		})
	}
}

func TestWithKeepaliveTimeoutIgnoresNonPositive(t *testing.T) {
	t.Parallel()

	o := defaultOptions()
	WithKeepaliveTimeout(0)(&o)
	WithKeepaliveTimeout(-1 * time.Second)(&o)
	if o.keepaliveTimeout != 0 {
		t.Fatalf("keepaliveTimeout = %s, want 0 (non-positive ignored)", o.keepaliveTimeout)
	}

	WithKeepaliveTimeout(4 * time.Second)(&o)
	if o.keepaliveTimeout != 4*time.Second {
		t.Fatalf("keepaliveTimeout = %s, want 4s", o.keepaliveTimeout)
	}
}

func TestHostKeyOptionsTrackInsecureFlag(t *testing.T) {
	t.Parallel()

	if o := defaultOptions(); !o.insecureHostKey {
		t.Fatalf("default insecureHostKey = false, want true")
	}

	o := defaultOptions()
	WithHostKeyCallback(nil)(&o)
	if !o.insecureHostKey {
		t.Fatalf("nil callback should be ignored and keep insecureHostKey = true")
	}

	WithHostKeyCallback(insecureIgnoreHostKey())(&o)
	if o.insecureHostKey {
		t.Fatalf("explicit callback should clear insecureHostKey")
	}

	WithInsecureIgnoreHostKey()(&o)
	if !o.insecureHostKey {
		t.Fatalf("WithInsecureIgnoreHostKey should set insecureHostKey = true")
	}
}
