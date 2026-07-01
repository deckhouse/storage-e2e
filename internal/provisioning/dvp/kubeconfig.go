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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func rewriteKubeconfigServer(kubeconfig []byte, localAddr string) ([]byte, error) {
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

func buildRestConfigFromKubeconfig(kubeconfig []byte) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}

	overrides := &clientcmd.ConfigOverrides{Timeout: (2 * time.Minute).String()}
	restConfig, err := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("creating client config: %w", err)
	}

	configureTunnelTimeouts(restConfig)
	return restConfig, nil
}

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

func publicKeyFromPrivateKey(privateKeyPEM []byte, passphrase string) (string, error) {
	signer, err := parsePrivateKeySigner(privateKeyPEM, passphrase)
	if err != nil {
		return "", err
	}
	authorized := ssh.MarshalAuthorizedKey(signer.PublicKey())
	key := strings.TrimSpace(string(authorized))
	if key == "" {
		return "", fmt.Errorf("derived SSH public key is empty")
	}
	return key, nil
}

func parsePrivateKeySigner(raw []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(raw)
	if err == nil {
		return signer, nil
	}
	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); !ok {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	if passphrase == "" {
		return nil, fmt.Errorf("private key is passphrase-protected but no passphrase was provided")
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(raw, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("decrypt private key with passphrase: %w", err)
	}
	return signer, nil
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
