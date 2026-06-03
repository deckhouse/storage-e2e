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

package commander

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapStatusToPhase(t *testing.T) {
	cases := []struct {
		in   string
		want ClusterPhase
	}{
		{"in_sync", ClusterPhaseReady},
		{"insync", ClusterPhaseReady},
		{"ready", ClusterPhaseReady},
		{"running", ClusterPhaseReady},
		{"active", ClusterPhaseReady},
		{"new", ClusterPhaseDraft},
		{"creating", ClusterPhaseCreating},
		{"provisioning", ClusterPhaseCreating},
		{"bootstrapping", ClusterPhaseCreating},
		{"updating", ClusterPhaseUpdating},
		{"upgrading", ClusterPhaseUpdating},
		{"deleting", ClusterPhaseDeleting},
		{"terminating", ClusterPhaseDeleting},
		{"failed", ClusterPhaseFailed},
		{"error", ClusterPhaseFailed},
		{"joining", ClusterPhaseJoining},
		{"ready_to_join", ClusterPhaseReadyToJoin},
		{"readytojoin", ClusterPhaseReadyToJoin},

		// Case-insensitive.
		{"IN_SYNC", ClusterPhaseReady},
		{"Creating", ClusterPhaseCreating},

		// Unknown values pass through verbatim (no normalization).
		{"some-novel-state", ClusterPhase("some-novel-state")},
		{"", ClusterPhase("")},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := mapStatusToPhase(tc.in)
			if got != tc.want {
				t.Errorf("mapStatusToPhase(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBase64Encode(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"ascii", "user:token", "dXNlcjp0b2tlbg=="},
		{"unicode", "tëst", "dMOrc3Q="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := base64Encode(tc.in)
			if got != tc.want {
				t.Errorf("base64Encode(%q)=%q, want %q", tc.in, got, tc.want)
			}
			// Sanity: must round-trip via stdlib decoder.
			dec, err := base64.StdEncoding.DecodeString(got)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if string(dec) != tc.in {
				t.Errorf("round-trip mismatch: %q -> %q", tc.in, string(dec))
			}
		})
	}
}

func TestNewClientWithOptions_Validation(t *testing.T) {
	t.Run("empty baseURL errors", func(t *testing.T) {
		_, err := NewClientWithOptions("", "tok", ClientOptions{})
		if err == nil || !strings.Contains(err.Error(), "baseURL") {
			t.Errorf("want baseURL error, got %v", err)
		}
	})

	t.Run("empty token errors", func(t *testing.T) {
		_, err := NewClientWithOptions("https://x", "", ClientOptions{})
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Errorf("want token error, got %v", err)
		}
	})

	t.Run("defaults: x-auth-token + /api/v1", func(t *testing.T) {
		c, err := NewClientWithOptions("https://commander.example.com", "tok", ClientOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.authMethod != AuthMethodXAuthToken {
			t.Errorf("authMethod=%q, want %q", c.authMethod, AuthMethodXAuthToken)
		}
		if c.apiPrefix != "/api/v1" {
			t.Errorf("apiPrefix=%q, want /api/v1", c.apiPrefix)
		}
	})

	t.Run("trailing slashes trimmed from baseURL and apiPrefix", func(t *testing.T) {
		c, err := NewClientWithOptions("https://x/", "tok", ClientOptions{APIPrefix: "/api/"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.baseURL != "https://x" {
			t.Errorf("baseURL=%q, want %q", c.baseURL, "https://x")
		}
		if c.apiPrefix != "/api" {
			t.Errorf("apiPrefix=%q, want /api", c.apiPrefix)
		}
	})

	t.Run("explicit auth method is honored", func(t *testing.T) {
		c, err := NewClientWithOptions("https://x", "tok", ClientOptions{AuthMethod: AuthMethodBearer})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.authMethod != AuthMethodBearer {
			t.Errorf("authMethod=%q, want %q", c.authMethod, AuthMethodBearer)
		}
	})

	t.Run("bad CACertPath errors with file path", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "does-not-exist.pem")
		_, err := NewClientWithOptions("https://x", "tok", ClientOptions{CACertPath: bad})
		if err == nil || !strings.Contains(err.Error(), "CA certificate") {
			t.Errorf("want CA certificate error, got %v", err)
		}
	})

	t.Run("malformed CA cert content errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-a-cert.pem")
		if err := os.WriteFile(path, []byte("not pem data"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewClientWithOptions("https://x", "tok", ClientOptions{CACertPath: path})
		if err == nil || !strings.Contains(err.Error(), "parse CA certificate") {
			t.Errorf("want parse error, got %v", err)
		}
	})

	t.Run("InsecureSkipTLSVerify is configured", func(t *testing.T) {
		c, err := NewClientWithOptions("https://x", "tok", ClientOptions{InsecureSkipTLSVerify: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.httpClient == nil || c.httpClient.Transport == nil {
			t.Fatal("expected non-nil httpClient/transport")
		}
	})
}

