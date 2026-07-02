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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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
	result := fmt.Sprintf("https://%s", localAddr)

	restConfig, err := buildRestConfig([]byte(sampleKubeconfig), localAddr)
	if err != nil {
		t.Fatalf("buildRestConfig() = %v, want nil", err)
	}
	if restConfig.Host != result {
		t.Fatalf("Host = %q, want %q", restConfig.Host, result)
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

func TestRewriteKubeconfigServer(t *testing.T) {
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
			out, err := rewriteKubeconfigServer([]byte(tt.in), localAddr)
			if err != nil {
				t.Fatalf("rewriteKubeconfigServer() error = %v", err)
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

func TestRewriteKubeconfigServerPreservesIndent(t *testing.T) {
	t.Parallel()
	in := "clusters:\n- cluster:\n    server: https://10.0.0.1:6443\n"
	out, err := rewriteKubeconfigServer([]byte(in), "127.0.0.1:1234")
	if err != nil {
		t.Fatalf("rewriteKubeconfigServer() error = %v", err)
	}
	if !strings.Contains(string(out), "    server: https://127.0.0.1:1234") {
		t.Errorf("indentation not preserved:\n%s", out)
	}
}

func TestRewriteKubeconfigServerNoServer(t *testing.T) {
	t.Parallel()
	if _, err := rewriteKubeconfigServer([]byte("apiVersion: v1\nkind: Config\n"), "127.0.0.1:1234"); err == nil {
		t.Fatal("rewriteKubeconfigServer() error = nil, want error when no server field present")
	}
}

func TestBuildRestConfigFromKubeconfig(t *testing.T) {
	t.Parallel()
	rewritten, err := rewriteKubeconfigServer([]byte(sampleKubeconfig), "127.0.0.1:40001")
	if err != nil {
		t.Fatalf("rewriteKubeconfigServer() error = %v", err)
	}
	restConfig, err := buildRestConfigFromKubeconfig(rewritten)
	if err != nil {
		t.Fatalf("buildRestConfigFromKubeconfig() error = %v", err)
	}
	if restConfig.Host != "https://127.0.0.1:40001" {
		t.Errorf("Host = %q, want https://127.0.0.1:40001", restConfig.Host)
	}
	if restConfig.WrapTransport == nil {
		t.Error("WrapTransport = nil, want tunnel timeouts applied")
	}
}

func TestPublicKeyFromPrivateKeyInline(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(block)

	got, err := publicKeyFromPrivateKey(pemBytes, "")
	if err != nil {
		t.Fatalf("publicKeyFromPrivateKey() = %v, want nil", err)
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if got != want {
		t.Errorf("public key = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "ssh-ed25519 ") {
		t.Errorf("public key = %q, want ssh-ed25519 prefix", got)
	}
}

func TestPublicKeyFromPrivateKeyEncryptedWithPassphrase(t *testing.T) {
	t.Parallel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("s3cret"))
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(block)

	if _, err := publicKeyFromPrivateKey(pemBytes, "s3cret"); err != nil {
		t.Errorf("with correct passphrase: %v, want nil", err)
	}
	if _, err := publicKeyFromPrivateKey(pemBytes, ""); err == nil {
		t.Error("missing passphrase: err = nil, want error")
	}
}
