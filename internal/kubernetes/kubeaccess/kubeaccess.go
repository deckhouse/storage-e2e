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

// Package kubeaccess bundles the kubeconfig/rest.Config plumbing shared by the
// cluster providers (dvp, commander): fetching a kubeconfig off a master over
// SSH, rewriting its server address, building rest.Configs with tunnel-friendly
// transport timeouts, opening an API tunnel, and probing direct reachability.
package kubeaccess

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

// getKubeconfigRemoteShell prints the master's kubeconfig, preferring
// /etc/kubernetes/super-admin.conf (Kubernetes 1.29+) and falling back to
// admin.conf. The two `sudo -n /bin/cat` invocations are intentionally not
// wrapped in a shell so a passwordless-sudo rule can target /bin/cat directly.
const getKubeconfigRemoteShell = "sudo -n /bin/cat /etc/kubernetes/super-admin.conf 2>/dev/null " +
	"|| sudo -n /bin/cat /etc/kubernetes/admin.conf"

// Executor runs a command on a remote host (typically over SSH).
type Executor interface {
	Exec(ctx context.Context, cmd string) (ssh.ExecResult, error)
}

// FetchKubeconfig reads the admin kubeconfig off the master reachable through
// exec. A non-zero exit (neither super-admin.conf nor admin.conf readable) is
// surfaced with the captured stderr for diagnosis.
func FetchKubeconfig(ctx context.Context, exec Executor) ([]byte, error) {
	res, err := exec.Exec(ctx, getKubeconfigRemoteShell)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig over ssh: %w (stderr: %s)",
			err, strings.TrimSpace(string(res.Stderr)))
	}
	if len(res.Stdout) == 0 {
		return nil, fmt.Errorf("empty kubeconfig from master (stderr: %s)",
			strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

// RewriteServer replaces every `server:` field in the kubeconfig with
// https://localAddr, preserving indentation. Used to point a fetched
// kubeconfig at the local end of an SSH tunnel.
func RewriteServer(kubeconfig []byte, localAddr string) ([]byte, error) {
	lines := strings.Split(string(kubeconfig), "\n")
	replaced := false
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "server:") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = fmt.Sprintf("%sserver: https://%s", indent, localAddr)
		replaced = true
	}
	if !replaced {
		return nil, fmt.Errorf("no server field found in kubeconfig")
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// BuildRestConfig parses the kubeconfig and overrides its server with
// https://overrideAddr (the local end of an SSH tunnel), applying
// tunnel-friendly transport timeouts.
func BuildRestConfig(kubeconfig []byte, overrideAddr string) (*rest.Config, error) {
	return buildRestConfig(kubeconfig, &clientcmd.ConfigOverrides{
		ClusterInfo: clientcmdapi.Cluster{
			Server: fmt.Sprintf("https://%s", overrideAddr),
		},
		Timeout: (2 * time.Minute).String(),
	})
}

// BuildRestConfigDirect parses the kubeconfig as-is (no server override),
// applying the same transport timeouts.
func BuildRestConfigDirect(kubeconfig []byte) (*rest.Config, error) {
	return buildRestConfig(kubeconfig, &clientcmd.ConfigOverrides{
		Timeout: (2 * time.Minute).String(),
	})
}

func buildRestConfig(kubeconfig []byte, overrides *clientcmd.ConfigOverrides) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("creating client config: %w", err)
	}

	configureTunnelTimeouts(restConfig)
	return restConfig, nil
}

// configureTunnelTimeouts caps per-phase HTTP timeouts so a hung SSH tunnel
// surfaces as an error instead of blocking client-go indefinitely.
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

// TunnelRestConfig opens an SSH tunnel through sshClient to remotePort on the
// last hop and returns a rest.Config built from the kubeconfig with its server
// pointed at the tunnel's local end, plus a close function for the tunnel.
func TunnelRestConfig(ctx context.Context, sshClient *ssh.Client, kubeconfig []byte, remotePort int) (*rest.Config, func() error, error) {
	tun, err := sshClient.OpenTunnel(ctx, remotePort)
	if err != nil {
		return nil, nil, fmt.Errorf("creating tunnel: %w", err)
	}

	restConfig, err := BuildRestConfig(kubeconfig, tun.LocalAddr())
	if err != nil {
		_ = tun.Close()
		return nil, nil, fmt.Errorf("creating rest config: %w", err)
	}

	return restConfig, tun.Close, nil
}

// directProbeTimeout bounds the reachability probe: a reachable API server
// answers /version well within it, and an unreachable one should not delay
// the SSH-tunnel fallback for long.
const directProbeTimeout = 5 * time.Second

// DirectReachable reports whether the API server in cfg answers a /version
// request directly (no tunnel), within a short timeout and without retries.
func DirectReachable(ctx context.Context, cfg *rest.Config) bool {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, directProbeTimeout)
	defer cancel()

	body := client.Discovery().RESTClient().Get().AbsPath("/version").Do(probeCtx)
	return body.Error() == nil
}
