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

package dvp

import (
	"net/http"
	"testing"
	"time"

	"k8s.io/client-go/rest"
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
	restConfig, err := buildRestConfig([]byte(sampleKubeconfig), localAddr)
	if err != nil {
		t.Fatalf("buildRestConfig() = %v, want nil", err)
	}
	if restConfig.Host != localAddr {
		t.Fatalf("Host = %q, want %q", restConfig.Host, localAddr)
	}
	if restConfig.WrapTransport == nil {
		t.Fatalf("WrapTransport = nil, want tunnel timeouts applied")
	}
}

func TestBuildRestConfigInvalidKubeconfig(t *testing.T) {
	t.Parallel()

	if _, err := buildRestConfig([]byte("not a kubeconfig"), "127.0.0.1:6445"); err == nil {
		t.Fatalf("buildRestConfig() = nil, want error for invalid kubeconfig")
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
