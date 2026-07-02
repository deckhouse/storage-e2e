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

package commander

import (
	"fmt"
	"net/http"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// getKubeconfigRemoteShell prints the master's kubeconfig, preferring
// /etc/kubernetes/super-admin.conf (Kubernetes 1.29+) and falling back to
// admin.conf. The two `sudo -n /bin/cat` invocations are intentionally not
// wrapped in a shell so a passwordless-sudo rule can target /bin/cat directly.
// The printed kubeconfig points the API server at the node-local proxy
// (https://127.0.0.1:6445), so it is only usable through the SSH tunnel.
const getKubeconfigRemoteShell = "sudo -n /bin/cat /etc/kubernetes/super-admin.conf 2>/dev/null " +
	"|| sudo -n /bin/cat /etc/kubernetes/admin.conf"

// buildRestConfig parses the fetched kubeconfig and overrides its server with
// the local end of the SSH tunnel, so client-go talks to the master API through
// the tunnel.
func buildRestConfig(kubeconfig []byte, localAddr string) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	overrides := &clientcmd.ConfigOverrides{
		ClusterInfo: clientcmdapi.Cluster{
			Server: fmt.Sprintf("https://%s", localAddr),
		},
		Timeout: (2 * time.Minute).String(),
	}

	restConfig, err := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("creating client config: %w", err)
	}

	configureTunnelTimeouts(restConfig)
	return restConfig, nil
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
