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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestEndpointAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "host only gets default port", addr: "example.com", want: "example.com:22"},
		{name: "host with port preserved", addr: "example.com:2222", want: "example.com:2222"},
		{name: "ipv4 with port", addr: "10.0.0.1:6443", want: "10.0.0.1:6443"},
		{name: "empty stays empty", addr: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := Endpoint{User: "u", Addr: tc.addr}
			if got := e.addr(); got != tc.want {
				t.Fatalf("addr() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEndpointClientConfigKeyData(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plain, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal plain key: %v", err)
	}
	plainPEM := pem.EncodeToMemory(plain)

	encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("s3cret"))
	if err != nil {
		t.Fatalf("marshal encrypted key: %v", err)
	}
	encryptedPEM := pem.EncodeToMemory(encrypted)

	hostKey := ssh.InsecureIgnoreHostKey()

	t.Run("plain key bytes produce a signer", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")
		e := Endpoint{User: "u", Addr: "host", KeyData: plainPEM}
		cfg, closer, err := e.clientConfig(context.Background(), hostKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if closer != nil {
			_ = closer.Close()
		}
		if cfg.User != "u" {
			t.Fatalf("User = %q, want u", cfg.User)
		}
		if len(cfg.Auth) != 1 {
			t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
		}
	})

	t.Run("encrypted key bytes with passphrase produce a signer", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")
		e := Endpoint{User: "u", Addr: "host", KeyData: encryptedPEM, Passphrase: "s3cret"}
		cfg, closer, err := e.clientConfig(context.Background(), hostKey)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if closer != nil {
			_ = closer.Close()
		}
		if len(cfg.Auth) != 1 {
			t.Fatalf("expected one auth method, got %d", len(cfg.Auth))
		}
	})

	t.Run("no key data and no agent fails", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")
		e := Endpoint{User: "u", Addr: "host"}
		if _, _, err := e.clientConfig(context.Background(), hostKey); err == nil {
			t.Fatalf("expected error when no credentials are available")
		}
	})
}

func TestParseSigner(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plain, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal plain key: %v", err)
	}
	plainPEM := pem.EncodeToMemory(plain)

	encrypted, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("s3cret"))
	if err != nil {
		t.Fatalf("marshal encrypted key: %v", err)
	}
	encryptedPEM := pem.EncodeToMemory(encrypted)

	t.Run("plain key parses", func(t *testing.T) {
		signer, err := parseSigner(plainPEM, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signer == nil {
			t.Fatalf("expected a signer, got nil")
		}
	})

	t.Run("encrypted with explicit passphrase parses", func(t *testing.T) {
		signer, err := parseSigner(encryptedPEM, "s3cret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signer == nil {
			t.Fatalf("expected a signer, got nil")
		}
	})

	t.Run("garbage fails", func(t *testing.T) {
		if _, err := parseSigner([]byte("not a key"), ""); err == nil {
			t.Fatalf("expected error for garbage input")
		}
	})
}
