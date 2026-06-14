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
	"k8s.io/client-go/tools/clientcmd"
)

// readKubeconfig loads the user-supplied base cluster kubeconfig from disk.
// The path may contain ~ and ${VAR} placeholders, which are expanded here.
func readKubeconfig(path string) ([]byte, error) {
	resolved, err := expandKubeconfigPath(path)
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

// expandKubeconfigPath resolves ${VAR} and a leading ~ in a kubeconfig path.
func expandKubeconfigPath(path string) (string, error) {
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

// loadKubeconfigViaTunnel reads the user-supplied base cluster kubeconfig from
// kubeconfigSrcPath, rewrites its API server to the local end of an SSH tunnel
// (127.0.0.1:localPort), writes the result under kubeconfigDir (named after
// host), and returns the ready-to-use client config along with the on-disk
// path. It owns no live resources, so there is nothing to release.
func loadKubeconfigViaTunnel(localPort int, kubeconfigDir, host, kubeconfigSrcPath string) (*rest.Config, string, error) {
	raw, err := readKubeconfig(kubeconfigSrcPath)
	if err != nil {
		return nil, "", fmt.Errorf("load base cluster kubeconfig: %w", err)
	}

	path, err := kubeconfigFilePath(kubeconfigDir, host)
	if err != nil {
		return nil, "", err
	}

	server := fmt.Sprintf("https://127.0.0.1:%d", localPort)
	cfg, err := buildKubeconfig(raw, server, path)
	if err != nil {
		return nil, "", fmt.Errorf("build kubeconfig: %w", err)
	}
	return cfg, path, nil
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
