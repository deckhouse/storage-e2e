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

package kubeaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

const sampleKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: base
  cluster:
    server: https://original.example.com:6443
    insecure-skip-tls-verify: true
contexts:
- name: ctx
  context:
    cluster: base
    user: admin
current-context: ctx
users:
- name: admin
  user:
    token: secret-token
`

func TestBuildRestConfigOverridesServer(t *testing.T) {
	t.Parallel()

	const localAddr = "127.0.0.1:6445"
	want := fmt.Sprintf("https://%s", localAddr)

	restConfig, err := BuildRestConfig([]byte(sampleKubeconfig), localAddr)
	if err != nil {
		t.Fatalf("BuildRestConfig() = %v, want nil", err)
	}
	if restConfig.Host != want {
		t.Fatalf("Host = %q, want %q", restConfig.Host, want)
	}
	if restConfig.WrapTransport == nil {
		t.Fatalf("WrapTransport = nil, want tunnel timeouts applied")
	}
}

func TestBuildRestConfigInvalidKubeconfig(t *testing.T) {
	t.Parallel()

	if _, err := BuildRestConfig([]byte("not a kubeconfig"), "127.0.0.1:6445"); err == nil {
		t.Fatalf("BuildRestConfig() = nil, want error for invalid kubeconfig")
	}
}

func TestBuildRestConfigDirectKeepsServer(t *testing.T) {
	t.Parallel()

	restConfig, err := BuildRestConfigDirect([]byte(sampleKubeconfig))
	if err != nil {
		t.Fatalf("BuildRestConfigDirect() error = %v", err)
	}
	if restConfig.Host != "https://original.example.com:6443" {
		t.Errorf("Host = %q, want the original server untouched", restConfig.Host)
	}
	if restConfig.WrapTransport == nil {
		t.Error("WrapTransport = nil, want tunnel timeouts applied")
	}
}

func TestConfigureTunnelTimeouts(t *testing.T) {
	t.Parallel()

	cfg := &rest.Config{}
	configureTunnelTimeouts(cfg)
	if cfg.WrapTransport == nil {
		t.Fatalf("WrapTransport = nil, want it to be set")
	}

	wrapped := cfg.WrapTransport(&http.Transport{})
	transport, ok := wrapped.(*http.Transport)
	if !ok {
		t.Fatalf("wrapped transport type = %T, want *http.Transport", wrapped)
	}
	if transport.TLSHandshakeTimeout != 30*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 30s", transport.TLSHandshakeTimeout)
	}
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 60s", transport.ResponseHeaderTimeout)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", transport.IdleConnTimeout)
	}
}

func TestRewriteServer(t *testing.T) {
	t.Parallel()

	const localAddr = "127.0.0.1:34567"
	tests := []struct {
		name string
		in   string
	}{
		{"standard node IP", "clusters:\n- cluster:\n    server: https://10.10.1.5:6443\n"},
		{"loopback proxy port", "clusters:\n- cluster:\n    server: https://127.0.0.1:6445\n"},
		{"quoted url", "clusters:\n- cluster:\n    server: \"https://10.0.0.1:6443\"\n"},
		{"tab indent", "clusters:\n- cluster:\n\t\tserver: https://host:6443\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := RewriteServer([]byte(tt.in), localAddr)
			if err != nil {
				t.Fatalf("RewriteServer() error = %v", err)
			}
			s := string(out)
			want := "server: https://" + localAddr
			if !strings.Contains(s, want) {
				t.Errorf("rewritten kubeconfig missing %q:\n%s", want, s)
			}
			if strings.Contains(s, ":6443") || strings.Contains(s, ":6445") {
				t.Errorf("original server URL should be gone:\n%s", s)
			}
		})
	}
}

func TestRewriteServerPreservesIndent(t *testing.T) {
	t.Parallel()
	in := "clusters:\n- cluster:\n    server: https://10.0.0.1:6443\n"
	out, err := RewriteServer([]byte(in), "127.0.0.1:1234")
	if err != nil {
		t.Fatalf("RewriteServer() error = %v", err)
	}
	if !strings.Contains(string(out), "    server: https://127.0.0.1:1234") {
		t.Errorf("indentation not preserved:\n%s", out)
	}
}

func TestRewriteServerNoServer(t *testing.T) {
	t.Parallel()
	if _, err := RewriteServer([]byte("apiVersion: v1\nkind: Config\n"), "127.0.0.1:1234"); err == nil {
		t.Fatal("RewriteServer() error = nil, want error when no server field present")
	}
}

func TestRewriteServerThenBuildDirect(t *testing.T) {
	t.Parallel()
	rewritten, err := RewriteServer([]byte(sampleKubeconfig), "127.0.0.1:40001")
	if err != nil {
		t.Fatalf("RewriteServer() error = %v", err)
	}
	restConfig, err := BuildRestConfigDirect(rewritten)
	if err != nil {
		t.Fatalf("BuildRestConfigDirect() error = %v", err)
	}
	if restConfig.Host != "https://127.0.0.1:40001" {
		t.Errorf("Host = %q, want https://127.0.0.1:40001", restConfig.Host)
	}
	if restConfig.WrapTransport == nil {
		t.Error("WrapTransport = nil, want tunnel timeouts applied")
	}
}

type fakeExecutor struct {
	res ssh.ExecResult
	err error
}

func (f fakeExecutor) Exec(context.Context, string) (ssh.ExecResult, error) {
	return f.res, f.err
}

func TestFetchKubeconfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		exec    fakeExecutor
		want    string
		wantErr bool
	}{
		{
			name: "success",
			exec: fakeExecutor{res: ssh.ExecResult{Stdout: []byte("kubeconfig-bytes")}},
			want: "kubeconfig-bytes",
		},
		{
			name:    "exec error surfaces stderr",
			exec:    fakeExecutor{res: ssh.ExecResult{Stderr: []byte("sudo: denied")}, err: errors.New("exit 1")},
			wantErr: true,
		},
		{
			name:    "empty stdout",
			exec:    fakeExecutor{res: ssh.ExecResult{}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := FetchKubeconfig(context.Background(), tt.exec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("FetchKubeconfig() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchKubeconfig() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("kubeconfig = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDirectReachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"33"}`))
	}))
	defer srv.Close()

	if !DirectReachable(context.Background(), &rest.Config{Host: srv.URL}) {
		t.Error("DirectReachable = false for a live API endpoint, want true")
	}
}

func TestDirectReachableUnreachable(t *testing.T) {
	t.Parallel()

	// A closed port: the probe must fail fast instead of hanging.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	deadURL := srv.URL
	srv.Close()

	start := time.Now()
	if DirectReachable(context.Background(), &rest.Config{Host: deadURL}) {
		t.Error("DirectReachable = true for a closed endpoint, want false")
	}
	if elapsed := time.Since(start); elapsed > directProbeTimeout+2*time.Second {
		t.Errorf("probe took %v, want bounded by ~%v", elapsed, directProbeTimeout)
	}
}
