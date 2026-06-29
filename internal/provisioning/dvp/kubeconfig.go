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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

func readKubeconfig(path string) ([]byte, error) {
	resolved, err := expandUserPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig %q: %w", resolved, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, fmt.Errorf("kubeconfig %q is empty", resolved)
	}
	return raw, nil
}

// readSSHPublicKey reads the OpenSSH public key that sits next to the given
// private key path (privateKeyPath + ".pub"). The key is injected into the VM
// cloud-init so the provisioner can later reach the VMs over SSH using the same
// key pair that connects to the base cluster.
func readSSHPublicKey(privateKeyPath string) (string, error) {
	path, err := expandUserPath(privateKeyPath + ".pub")
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read SSH public key %q: %w", path, err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("SSH public key %q is empty", path)
	}
	return key, nil
}

func expandUserPath(path string) (string, error) {
	expanded := os.ExpandEnv(path)
	if !strings.HasPrefix(expanded, "~") {
		return expanded, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for %q: %w", path, err)
	}
	if expanded == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(expanded, "~/")), nil
}

func configureTunnelTimeouts(cfg *rest.Config) {
	prev := cfg.WrapTransport
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prev != nil {
			rt = prev(rt)
		}
		if t, ok := rt.(*http.Transport); ok {
			t = t.Clone()
			t.TLSHandshakeTimeout = 30 * time.Second
			t.ResponseHeaderTimeout = 60 * time.Second
			t.IdleConnTimeout = 90 * time.Second
			return t
		}
		return rt
	}
}
