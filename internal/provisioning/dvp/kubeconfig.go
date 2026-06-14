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
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
)

const kubeconfigReadCommand = "sudo -n /bin/cat /etc/kubernetes/super-admin.conf 2>/dev/null " +
	"|| sudo -n /bin/cat /etc/kubernetes/admin.conf"

func fetchKubeconfig(ctx context.Context, sshClient ssh.SSHClient) ([]byte, error) {
	stdout, stderr, err := sshClient.ExecCapture(ctx, kubeconfigReadCommand)
	if err != nil {
		return nil, fmt.Errorf("%w (remote stderr: %s)", err, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(stdout) == "" {
		return nil, fmt.Errorf("empty kubeconfig output (remote stderr: %s)", strings.TrimSpace(stderr))
	}
	return []byte(stdout), nil
}

func buildKubeconfig(raw []byte, server, path string) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	for _, cluster := range apiCfg.Clusters {
		cluster.Server = server
	}

	if err := clientcmd.WriteToFile(*apiCfg, path); err != nil {
		return nil, fmt.Errorf("write kubeconfig %q: %w", path, err)
	}

	restCfg, err := clientcmd.NewDefaultClientConfig(*apiCfg, &clientcmd.ConfigOverrides{}).ClientConfig()

	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	configureTunnelTimeouts(restCfg)
	return restCfg, nil
}

func kubeconfigFilePath(dir, host string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create kubeconfig dir %q: %w", dir, err)
	}
	return filepath.Join(dir, fmt.Sprintf("kubeconfig-%s.yml", host)), nil
}

func configureTunnelTimeouts(cfg *rest.Config) {
	cfg.Timeout = 2 * time.Minute

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
