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
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

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
