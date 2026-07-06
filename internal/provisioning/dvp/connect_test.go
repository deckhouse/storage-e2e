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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestConnectDirectKubeconfigSkipsTunnel verifies the auto-detect: when the
// base cluster kubeconfig is directly reachable, Connect returns a direct
// rest.Config without dialing SSH (the connector has no usable SSH config, so
// reaching the tunnel path would fail the test).
func TestConnectDirectKubeconfigSkipsTunnel(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"33"}`))
	}))
	defer srv.Close()

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: base
  cluster:
    server: %s
contexts:
- name: ctx
  context:
    cluster: base
    user: admin
current-context: ctx
users:
- name: admin
  user:
    token: secret-token
`, srv.URL)

	c := newConnector(
		&Config{},
		Credentials{Kubeconfig: []byte(kubeconfig)},
		slog.New(slog.DiscardHandler),
	)

	restConfig, cleanup, err := c.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v, want direct connection", err)
	}
	defer cleanup()

	if restConfig.Host != srv.URL {
		t.Errorf("Host = %q, want the direct server %q (no tunnel rewrite)", restConfig.Host, srv.URL)
	}
}
