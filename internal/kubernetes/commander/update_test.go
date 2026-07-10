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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestUpdateCluster(t *testing.T) {
	t.Run("PUT carries current_revision and values", func(t *testing.T) {
		var recs []recordedRequest
		srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"c1","name":"sys","current_revision":6}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.UpdateCluster(context.Background(), "c1", UpdateClusterRequest{
			CurrentRevision: 5,
			Values:          map[string]interface{}{"prefix": "sys", "masterCount": 1},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "c1" || got.CurrentRevision != 6 {
			t.Errorf("decoded %+v", got)
		}
		if len(recs) != 1 {
			t.Fatalf("got %d requests, want 1", len(recs))
		}
		r := recs[0]
		if r.Method != http.MethodPut || r.Path != "/api/v1/clusters/c1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.Path)
		}
		var body UpdateClusterRequest
		if err := json.Unmarshal(r.BodyBytes, &body); err != nil {
			t.Fatalf("body not JSON: %v (raw=%s)", err, string(r.BodyBytes))
		}
		if body.CurrentRevision != 5 {
			t.Errorf("current_revision=%d, want 5", body.CurrentRevision)
		}
		if body.Values["masterCount"] != float64(1) {
			t.Errorf("masterCount not forwarded: %+v", body.Values)
		}
	})

	t.Run("409 is a revision conflict", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"revision_conflict"}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		_, err := c.UpdateCluster(context.Background(), "c1", UpdateClusterRequest{CurrentRevision: 1})
		if !errors.Is(err, ErrRevisionConflict) {
			t.Errorf("want ErrRevisionConflict, got %v", err)
		}
	})
}

func TestListClusterChangeRequests(t *testing.T) {
	t.Run("filters by cluster_id and decodes array", func(t *testing.T) {
		var recs []recordedRequest
		srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"id":"cr1","cluster_id":"c1","status":"pending"}]`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.ListClusterChangeRequests(context.Background(), "c1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "cr1" {
			t.Fatalf("decoded %+v", got)
		}
		if recs[0].Path != "/api/v1/cluster_change_requests" || recs[0].RawQuery != "cluster_id=c1" {
			t.Errorf("unexpected request: %s?%s", recs[0].Path, recs[0].RawQuery)
		}
	})

	t.Run("decodes items-wrapped list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"id":"cr2"}]}`))
		}))
		defer srv.Close()

		c := mustClient(t, srv.URL, "tok", ClientOptions{})
		got, err := c.ListClusterChangeRequests(context.Background(), "c1")
		if err != nil || len(got) != 1 || got[0].ID != "cr2" {
			t.Fatalf("decoded %+v (err=%v)", got, err)
		}
	})
}

func TestApproveClusterChangeRequest(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{name: "200 OK", status: http.StatusOK, body: `{}`, wantErr: nil},
		{name: "already approved is idempotent", status: http.StatusConflict, body: `{"error":"already_approved"}`, wantErr: nil},
		{name: "already completed is idempotent", status: http.StatusConflict, body: `{"error":"already_completed"}`, wantErr: nil},
		{name: "cannot converge", status: http.StatusConflict, body: `{"error":"cluster_cannot_be_converged"}`, wantErr: ErrClusterCannotConverge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recs []recordedRequest
			srv := httptest.NewServer(captureHandler(&recs, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := mustClient(t, srv.URL, "tok", ClientOptions{})
			err := c.ApproveClusterChangeRequest(context.Background(), "cr1")
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if recs[0].Method != http.MethodPost || recs[0].Path != "/api/v1/cluster_change_requests/cr1/approve" {
				t.Errorf("unexpected request: %s %s", recs[0].Method, recs[0].Path)
			}
		})
	}
}

func TestUpdateClusterValues_MergesAndRetriesOnConflict(t *testing.T) {
	var mu sync.Mutex
	putCount := 0
	var lastPut UpdateClusterRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clusters":
			_, _ = w.Write([]byte(`[{"id":"c1","name":"sys","current_revision":5,"values":{"prefix":"sys","masterCount":3,"foo":"bar"}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/clusters/c1":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &lastPut)
			putCount++
			if putCount == 1 {
				w.WriteHeader(http.StatusConflict) // force one revision-conflict retry
				_, _ = w.Write([]byte(`{"error":"revision_conflict"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","name":"sys","current_revision":6}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	_, err := c.UpdateClusterValues(context.Background(), "sys", func(values map[string]interface{}) {
		values["masterCount"] = 1
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if putCount != 2 {
		t.Errorf("PUT called %d times, want 2 (one conflict retry)", putCount)
	}
	// The change is merged onto the existing values (prefix/foo preserved).
	if lastPut.Values["masterCount"] != float64(1) {
		t.Errorf("masterCount=%v, want 1", lastPut.Values["masterCount"])
	}
	if lastPut.Values["prefix"] != "sys" || lastPut.Values["foo"] != "bar" {
		t.Errorf("existing values not preserved: %+v", lastPut.Values)
	}
	if lastPut.CurrentRevision != 5 {
		t.Errorf("current_revision=%d, want 5 (re-fetched)", lastPut.CurrentRevision)
	}
}

func TestSetClusterInputValueAndWait_ApprovesAndConverges(t *testing.T) {
	var mu sync.Mutex
	approved := false
	approveHits := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clusters":
			status := "updating"
			if approved {
				status = "in_sync"
			}
			_, _ = w.Write([]byte(`[{"id":"c1","name":"sys","current_revision":5,"status":"` + status + `","values":{"prefix":"sys","masterCount":3}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/clusters/c1":
			_, _ = w.Write([]byte(`{"id":"c1","name":"sys","current_revision":6,"status":"updating"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cluster_change_requests":
			if approved {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"cr1","cluster_id":"c1","status":"pending"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cluster_change_requests/cr1/approve":
			approved = true
			approveHits++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := mustClient(t, srv.URL, "tok", ClientOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := c.SetClusterInputValueAndWait(ctx, "sys", "masterCount", 1, 20*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approveHits != 1 {
		t.Errorf("approve hit %d times, want 1", approveHits)
	}
}
