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

// HTTP-driven tests for the Commander API client. Each test spins up an
// httptest.Server, points a real Client at it, and asserts that requests are
// shaped correctly and responses are decoded as expected. No network traffic
// leaves the test binary.

package commander

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// recordedRequest captures the parts of an *http.Request we care about so
// tests can assert against them after the response is returned.
type recordedRequest struct {
	Method      string
	Path        string
	RawQuery    string
	Header      http.Header
	BodyBytes   []byte
	CookieToken string
}

// captureHandler returns an http.Handler that records every incoming request
// into *out and calls fn to write the response. fn may inspect r to vary the
// behavior per call.
func captureHandler(out *[]recordedRequest, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recordedRequest{
			Method:    r.Method,
			Path:      r.URL.Path,
			RawQuery:  r.URL.RawQuery,
			Header:    r.Header.Clone(),
			BodyBytes: body,
		}
		if cookie, err := r.Cookie("token"); err == nil {
			rec.CookieToken = cookie.Value
		}
		*out = append(*out, rec)
		fn(w, r)
	}
}

func mustClient(t *testing.T, baseURL, token string, opts ClientOptions) *Client {
	t.Helper()
	c, err := NewClientWithOptions(baseURL, token, opts)
	if err != nil {
		t.Fatalf("NewClientWithOptions: %v", err)
	}
	return c
}

func TestSetAuthHeaders_AllMethods(t *testing.T) {
	cases := []struct {
		name      string
		method    AuthMethod
		user      string
		token     string
		assertReq func(t *testing.T, r recordedRequest)
	}{
		{
			name:   "default = X-Auth-Token",
			method: "",
			token:  "sekret",
			assertReq: func(t *testing.T, r recordedRequest) {
				if got := r.Header.Get("X-Auth-Token"); got != "sekret" {
					t.Errorf("X-Auth-Token=%q, want sekret", got)
				}
				if r.Header.Get("Authorization") != "" {
					t.Errorf("Authorization header should be empty for x-auth-token")
				}
			},
		},
		{
			name:   "bearer",
			method: AuthMethodBearer,
			token:  "sekret",
			assertReq: func(t *testing.T, r recordedRequest) {
				if got := r.Header.Get("Authorization"); got != "Bearer sekret" {
					t.Errorf("Authorization=%q, want Bearer sekret", got)
				}
			},
		},
		{
			name:   "token",
			method: AuthMethodToken,
			token:  "sekret",
			assertReq: func(t *testing.T, r recordedRequest) {
				if got := r.Header.Get("Authorization"); got != "Token sekret" {
					t.Errorf("Authorization=%q, want Token sekret", got)
				}
			},
		},
		{
			name:   "cookie",
			method: AuthMethodCookie,
			token:  "sekret",
			assertReq: func(t *testing.T, r recordedRequest) {
				if r.CookieToken != "sekret" {
					t.Errorf("cookie token=%q, want sekret", r.CookieToken)
				}
			},
		},
		{
			name:   "basic",
			method: AuthMethodBasic,
			user:   "alice",
			token:  "sekret",
			assertReq: func(t *testing.T, r recordedRequest) {
				want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:sekret"))
				if got := r.Header.Get("Authorization"); got != want {
					t.Errorf("Authorization=%q, want %q", got, want)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recs []recordedRequest
			srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"x","name":"y"}`))
			}))
			defer srv.Close()

			c := mustClient(t, srv.URL, tc.token, ClientOptions{
				AuthMethod: tc.method,
				AuthUser:   tc.user,
			})
			if _, err := c.GetClusterByID(context.Background(), "x"); err != nil {
				t.Fatalf("GetClusterByID: %v", err)
			}
			if len(recs) != 1 {
				t.Fatalf("got %d requests, want 1", len(recs))
			}
			tc.assertReq(t, recs[0])
			if r := recs[0]; r.Header.Get("Accept") != "application/json" {
				t.Errorf("Accept=%q, want application/json", r.Header.Get("Accept"))
			}
		})
	}
}

func TestGetClusterByID(t *testing.T) {
	t.Run("200 returns cluster", func(t *testing.T) {
		var recs []recordedRequest
		srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"abc","name":"my-cluster","status":"in_sync"}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.GetClusterByID(context.Background(), "abc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "abc" || got.Name != "my-cluster" || got.Status != "in_sync" {
			t.Errorf("decoded %+v", got)
		}
		if recs[0].Path != "/api/v1/clusters/abc" || recs[0].Method != http.MethodGet {
			t.Errorf("unexpected request: %+v", recs[0])
		}
	})

	t.Run("404 returns ErrClusterNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing", http.StatusNotFound)
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		_, err := c.GetClusterByID(context.Background(), "ghost")
		if !errors.Is(err, ErrClusterNotFound) {
			t.Errorf("want ErrClusterNotFound, got %v", err)
		}
	})

	t.Run("500 returns wrapped error with body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		_, err := c.GetClusterByID(context.Background(), "x")
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "500") || !strings.Contains(msg, "boom") {
			t.Errorf("error should include code and body: %v", err)
		}
	})
}

func TestListClustersAPI(t *testing.T) {
	t.Run("decodes array body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":"a","name":"alpha"},{"id":"b","name":"beta"}]`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.ListClustersAPI(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
			t.Errorf("decoded %+v", got)
		}
	})

	t.Run("decodes object body with items[]", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"id":"a","name":"alpha"}]}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.ListClustersAPI(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "alpha" {
			t.Errorf("decoded %+v", got)
		}
	})

	t.Run("decodes object body with data[]", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"c","name":"gamma"}]}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.ListClustersAPI(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "gamma" {
			t.Errorf("decoded %+v", got)
		}
	})

	t.Run("garbage body returns descriptive error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json at all`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		_, err := c.ListClustersAPI(context.Background())
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "failed to decode response") {
			t.Errorf("want 'failed to decode response' error, got %v", err)
		}
	})
}

func TestGetClusterByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"a","name":"alpha"},{"id":"b","name":"beta"}]`))
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})

	t.Run("found", func(t *testing.T) {
		got, err := c.GetClusterByName(context.Background(), "beta")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "b" {
			t.Errorf("ID=%q, want b", got.ID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := c.GetClusterByName(context.Background(), "zeta")
		if !errors.Is(err, ErrClusterNotFound) {
			t.Errorf("want ErrClusterNotFound, got %v", err)
		}
	})
}

func TestCreateClusterFromTemplate(t *testing.T) {
	var recs []recordedRequest
	srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new","name":"created"}`))
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	values := map[string]interface{}{"releaseChannel": "EarlyAccess"}
	got, err := c.CreateClusterFromTemplate(context.Background(), "my-cluster", "tpl-version-123", "reg-456", values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "new" || got.Name != "created" {
		t.Errorf("decoded %+v", got)
	}

	if len(recs) != 1 {
		t.Fatalf("got %d requests, want 1", len(recs))
	}
	r := recs[0]
	if r.Method != http.MethodPost || r.Path != "/api/v1/clusters" {
		t.Errorf("unexpected request: %s %s", r.Method, r.Path)
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}

	// Verify request body shape.
	var body CreateClusterRequest
	if err := json.Unmarshal(r.BodyBytes, &body); err != nil {
		t.Fatalf("body not JSON: %v (raw=%s)", err, string(r.BodyBytes))
	}
	if body.Name != "my-cluster" || body.ClusterTemplateVersionID != "tpl-version-123" || body.RegistryID != "reg-456" {
		t.Errorf("body fields: %+v", body)
	}
	if body.Values["releaseChannel"] != "EarlyAccess" {
		t.Errorf("Values not forwarded: %+v", body.Values)
	}
}

func TestDeleteClusterByID(t *testing.T) {
	t.Run("204 No Content is success", func(t *testing.T) {
		var recs []recordedRequest
		srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		if err := c.DeleteClusterByID(context.Background(), "xyz"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if recs[0].Method != http.MethodDelete || recs[0].Path != "/api/v1/clusters/xyz" {
			t.Errorf("unexpected request: %+v", recs[0])
		}
	})

	t.Run("202 Accepted is success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		if err := c.DeleteClusterByID(context.Background(), "xyz"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("500 returns error with body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("nope"))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		err := c.DeleteClusterByID(context.Background(), "xyz")
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "nope") {
			t.Errorf("want 500/nope in error, got %v", err)
		}
	})
}

func TestGetClusterKubeconfigByID(t *testing.T) {
	t.Run("raw kubeconfig body", func(t *testing.T) {
		const raw = "apiVersion: v1\nkind: Config\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/clusters/x/kubeconfig" {
				http.Error(w, "wrong path", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(raw))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.GetClusterKubeconfigByID(context.Background(), "x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != raw {
			t.Errorf("got %q, want %q", got, raw)
		}
	})

	t.Run("JSON wrapper with 'kubeconfig' field", func(t *testing.T) {
		const raw = "apiVersion: v1\nkind: Config\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			payload, _ := json.Marshal(map[string]string{"kubeconfig": raw})
			_, _ = w.Write(payload)
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.GetClusterKubeconfigByID(context.Background(), "x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != raw {
			t.Errorf("got %q, want %q", got, raw)
		}
	})

	t.Run("/kubeconfig 404 falls back to cluster-details ClusterAgentData.data.kubeconfig", func(t *testing.T) {
		const raw = "apiVersion: v1\nkind: Config\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/clusters/x/kubeconfig":
				http.Error(w, "no such endpoint", http.StatusNotFound)
			case "/api/v1/clusters/x":
				resp := ClusterResponse{
					ID:     "x",
					Status: "in_sync",
					ClusterAgentData: map[string]interface{}{
						"data": map[string]interface{}{"kubeconfig": raw},
					},
				}
				payload, _ := json.Marshal(resp)
				_, _ = w.Write(payload)
			default:
				http.Error(w, "unexpected", http.StatusBadRequest)
			}
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.GetClusterKubeconfigByID(context.Background(), "x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != raw {
			t.Errorf("got %q, want %q", got, raw)
		}
	})

	t.Run("/kubeconfig 404 with nothing in details returns descriptive error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/clusters/x/kubeconfig" {
				http.Error(w, "no such endpoint", http.StatusNotFound)
				return
			}
			// details lookup with no kubeconfig field at all
			_, _ = w.Write([]byte(`{"id":"x","status":"new"}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		_, err := c.GetClusterKubeconfigByID(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "kubeconfig not found") {
			t.Errorf("want 'kubeconfig not found' error, got %v", err)
		}
	})
}

func TestGetRegistryByName(t *testing.T) {
	registries := `[{"id":"r1","name":"prod-registry"},{"id":"r2","name":"dev-registry-eu"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(registries))
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})

	t.Run("exact name match wins", func(t *testing.T) {
		got, err := c.GetRegistryByName(context.Background(), "prod-registry")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "r1" {
			t.Errorf("ID=%q, want r1", got.ID)
		}
	})

	t.Run("partial match used when exact missing", func(t *testing.T) {
		got, err := c.GetRegistryByName(context.Background(), "dev-registry")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "r2" {
			t.Errorf("ID=%q, want r2 (partial match)", got.ID)
		}
	})

	t.Run("nothing matches errors", func(t *testing.T) {
		_, err := c.GetRegistryByName(context.Background(), "no-such-thing")
		if err == nil || !strings.Contains(err.Error(), "no-such-thing") {
			t.Errorf("want 'no-such-thing' error, got %v", err)
		}
	})
}

func TestGetClusterConnectionInfo_PrefersConnectionHosts(t *testing.T) {
	const kc = "apiVersion: v1\nkind: Config\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/clusters" && r.Method == http.MethodGet:
			// list returns one cluster
			resp := []ClusterResponse{{
				ID:     "c1",
				Name:   "my-cluster",
				Status: "in_sync",
				ConnectionHosts: map[string]interface{}{
					"api_endpoint": "https://api.example.com",
					"masters": []interface{}{
						map[string]interface{}{
							"host": "10.1.2.3",
							"user": "ubuntu",
							"port": float64(2222),
						},
					},
				},
			}}
			payload, _ := json.Marshal(resp)
			_, _ = w.Write(payload)
		case r.URL.Path == "/api/v1/clusters/c1/kubeconfig":
			_, _ = w.Write([]byte(kc))
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	info, err := c.GetClusterConnectionInfo(context.Background(), "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.APIEndpoint != "https://api.example.com" {
		t.Errorf("APIEndpoint=%q", info.APIEndpoint)
	}
	if info.SSHHost != "10.1.2.3" || info.SSHUser != "ubuntu" || info.SSHPort != 2222 {
		t.Errorf("SSH info wrong: %+v", info)
	}
	if info.Kubeconfig != kc {
		t.Errorf("kubeconfig not propagated")
	}
}

func TestGetClusterConnectionInfo_FallsBackToAgentData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/clusters" && r.Method == http.MethodGet:
			resp := []ClusterResponse{{
				ID:     "c1",
				Name:   "my-cluster",
				Status: "in_sync",
				ClusterAgentData: map[string]interface{}{
					"data": map[string]interface{}{
						"ssh_host": "10.9.9.9",
						"ssh_user": "root",
						// no port: default 22 should apply
					},
				},
			}}
			payload, _ := json.Marshal(resp)
			_, _ = w.Write(payload)
		case strings.HasSuffix(r.URL.Path, "/kubeconfig"):
			// no kubeconfig endpoint; let it fail
			http.Error(w, "missing", http.StatusNotFound)
		case r.URL.Path == "/api/v1/clusters/c1":
			// detail lookup used by fallback also has no kubeconfig
			_, _ = w.Write([]byte(`{"id":"c1","name":"my-cluster"}`))
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	info, err := c.GetClusterConnectionInfo(context.Background(), "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SSHHost != "10.9.9.9" || info.SSHUser != "root" {
		t.Errorf("agent-data fallback failed: %+v", info)
	}
	if info.SSHPort != 22 {
		t.Errorf("default port not applied, got %d", info.SSHPort)
	}
}

func TestGetClusterConnectionInfo_FallsBackToLegacyFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/clusters":
			resp := []ClusterResponse{{
				ID:      "c1",
				Name:    "my-cluster",
				SSHHost: "10.5.5.5",
				SSHUser: "deploy",
				SSHPort: 0,
			}}
			payload, _ := json.Marshal(resp)
			_, _ = w.Write(payload)
		case "/api/v1/clusters/c1/kubeconfig":
			http.Error(w, "missing", http.StatusNotFound)
		case "/api/v1/clusters/c1":
			_, _ = w.Write([]byte(`{"id":"c1","name":"my-cluster"}`))
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	info, err := c.GetClusterConnectionInfo(context.Background(), "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SSHHost != "10.5.5.5" || info.SSHUser != "deploy" || info.SSHPort != 22 {
		t.Errorf("legacy fallback failed: %+v", info)
	}
}

func TestGetClusterConnectionInfo_NoSSHNoKubeconfigErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/clusters":
			_, _ = w.Write([]byte(`[{"id":"c1","name":"my-cluster"}]`))
		case "/api/v1/clusters/c1/kubeconfig":
			http.Error(w, "no kubeconfig endpoint", http.StatusNotFound)
		case "/api/v1/clusters/c1":
			_, _ = w.Write([]byte(`{"id":"c1","name":"my-cluster"}`))
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	_, err := c.GetClusterConnectionInfo(context.Background(), "my-cluster")
	if err == nil {
		t.Fatal("expected error when neither kubeconfig nor SSH info is available")
	}
}

// Sanity: ensure srv.URL is a usable URL the client can talk to. This guards
// against test infra regressions where httptest.NewServer returns something
// pathological.
func TestServerURLIsValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if _, err := url.Parse(srv.URL); err != nil {
		t.Fatalf("httptest server URL is not parseable: %v", err)
	}
}
