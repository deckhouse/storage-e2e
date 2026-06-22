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

func TestExpandTilde(t *testing.T) {
	t.Parallel()

	t.Run("no tilde unchanged", func(t *testing.T) {
		t.Parallel()
		got, err := expandTilde("/etc/ssh/key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/etc/ssh/key" {
			t.Fatalf("got %q, want /etc/ssh/key", got)
		}
	})

	t.Run("tilde expands to home", func(t *testing.T) {
		t.Parallel()
		got, err := expandTilde("~/.ssh/id_ed25519")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == "~/.ssh/id_ed25519" {
			t.Fatalf("tilde was not expanded: %q", got)
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
