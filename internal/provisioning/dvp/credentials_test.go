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
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFromContent(t *testing.T) {
	t.Parallel()

	cfg := Config{
		SSHUser:           "user",
		SSHHost:           "host",
		KubeConfigContent: "kube-bytes",
		SSHKeyContent:     "key-bytes",
	}

	creds, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if string(creds.Kubeconfig) != "kube-bytes" {
		t.Errorf("Kubeconfig = %q, want kube-bytes", creds.Kubeconfig)
	}
	if string(creds.SSHKey) != "key-bytes" {
		t.Errorf("SSHKey = %q, want key-bytes", creds.SSHKey)
	}
	if creds.JumpKey != nil {
		t.Errorf("JumpKey = %q, want nil when no jump host", creds.JumpKey)
	}
}

func TestResolveFromFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	kubePath := filepath.Join(dir, "kubeconfig")
	keyPath := filepath.Join(dir, "id_key")
	if err := os.WriteFile(kubePath, []byte("kube-file"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key-file"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	cfg := Config{
		SSHUser:        "user",
		SSHHost:        "host",
		KubeConfigPath: kubePath,
		SSHKeyPath:     keyPath,
	}

	creds, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if string(creds.Kubeconfig) != "kube-file" {
		t.Errorf("Kubeconfig = %q, want kube-file", creds.Kubeconfig)
	}
	if string(creds.SSHKey) != "key-file" {
		t.Errorf("SSHKey = %q, want key-file", creds.SSHKey)
	}
}

func TestResolveExpandsEnvInPath(t *testing.T) {
	dir := t.TempDir()
	kubePath := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubePath, []byte("kube-env"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	t.Setenv("DVP_TEST_DIR", dir)

	cfg := Config{
		SSHUser:        "user",
		SSHHost:        "host",
		KubeConfigPath: "$DVP_TEST_DIR/kubeconfig",
		SSHKeyContent:  "key-bytes",
	}

	creds, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if string(creds.Kubeconfig) != "kube-env" {
		t.Errorf("Kubeconfig = %q, want kube-env", creds.Kubeconfig)
	}
}

func TestResolveReadErrorOnMissingFile(t *testing.T) {
	t.Parallel()

	cfg := Config{
		SSHUser:        "user",
		SSHHost:        "host",
		KubeConfigPath: filepath.Join(t.TempDir(), "does-not-exist"),
		SSHKeyContent:  "key-bytes",
	}

	if _, err := cfg.Resolve(); err == nil {
		t.Fatalf("Resolve() = nil, want error for missing kubeconfig file")
	}
}

func TestResolveRejectsEmptyKubeconfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		SSHUser:           "user",
		SSHHost:           "host",
		KubeConfigContent: "   \n\t ",
		SSHKeyContent:     "key-bytes",
	}

	if _, err := cfg.Resolve(); err == nil {
		t.Fatalf("Resolve() = nil, want error for empty kubeconfig")
	}
}

func TestResolveJumpKey(t *testing.T) {
	t.Parallel()

	cfg := Config{
		SSHUser:           "user",
		SSHHost:           "host",
		KubeConfigContent: "kube-bytes",
		SSHKeyContent:     "key-bytes",
		SSHJumpHost:       "jump",
		SSHJumpUser:       "jumpuser",
		SSHJumpKeyContent: "jump-key-bytes",
	}

	creds, err := cfg.Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if string(creds.JumpKey) != "jump-key-bytes" {
		t.Errorf("JumpKey = %q, want jump-key-bytes", creds.JumpKey)
	}
}
