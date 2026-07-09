/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package e2e

import (
	"context"
	"errors"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type fakeExecutor struct{}

func (fakeExecutor) Exec(context.Context, string, string) (ExecResult, error) {
	return ExecResult{}, nil
}

type fakeProvider struct {
	cluster *clusterprovider.Cluster
	openErr error
}

func (fakeProvider) Name() string                    { return "fake" }
func (fakeProvider) Bootstrap(context.Context) error { return nil }
func (fakeProvider) Remove(context.Context) error    { return nil }
func (p fakeProvider) ConnectTestCluster(context.Context) (*clusterprovider.Cluster, error) {
	return p.cluster, p.openErr
}

// bareProvider mimics a provider without ConnectTestCluster support (e.g. commander).
type bareProvider struct{}

func (bareProvider) Name() string                    { return "bare" }
func (bareProvider) Bootstrap(context.Context) error { return nil }
func (bareProvider) Remove(context.Context) error    { return nil }
func (bareProvider) ConnectTestCluster(context.Context) (*clusterprovider.Cluster, error) {
	return nil, clusterprovider.ErrConnectUnsupported
}

func testOptions() connectOptions {
	return newConnectOptions([]Option{WithoutLock(), WithoutHealthCheck()})
}

func validCluster(cleanupRan *bool) *clusterprovider.Cluster {
	return &clusterprovider.Cluster{
		RESTConfig: &rest.Config{},
		Nodes:      fakeExecutor{},
		Cleanup:    func() { *cleanupRan = true },
	}
}

func TestConnectWithProviderAssemblesCluster(t *testing.T) {
	cleanupRan := false
	provider := fakeProvider{cluster: validCluster(&cleanupRan)}

	cluster, err := connectWithProvider(context.Background(), provider, testOptions())
	if err != nil {
		t.Fatalf("connectWithProvider: %v", err)
	}
	if cluster.ProviderName() != "fake" {
		t.Errorf("ProviderName = %q, want fake", cluster.ProviderName())
	}
	if cluster.RESTConfig() == nil {
		t.Error("RESTConfig is nil")
	}
	if cluster.Nodes() == nil {
		t.Error("capability strategies are nil")
	}

	if err := cluster.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !cleanupRan {
		t.Error("Close did not run the cluster cleanup")
	}

	// Close is idempotent.
	cleanupRan = false
	if err := cluster.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if cleanupRan {
		t.Error("second Close re-ran the cluster cleanup")
	}
}

func TestConnectWithProviderRejectsUnsupportedConnect(t *testing.T) {
	_, err := connectWithProvider(context.Background(), bareProvider{}, testOptions())
	if !errors.Is(err, ErrConnectUnsupported) {
		t.Fatalf("err = %v, want ErrConnectUnsupported", err)
	}
}

func TestConnectWithProviderValidatesCluster(t *testing.T) {
	cases := []struct {
		name    string
		cluster *clusterprovider.Cluster
	}{
		{"missing rest config", &clusterprovider.Cluster{Nodes: fakeExecutor{}}},
		{"missing node executor", &clusterprovider.Cluster{RESTConfig: &rest.Config{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cluster.Cleanup = func() {}
			_, err := connectWithProvider(context.Background(), fakeProvider{cluster: tc.cluster}, testOptions())
			if err == nil {
				t.Fatal("expected an error for an incomplete cluster")
			}
		})
	}
}

func TestConnectWithProviderPropagatesOpenError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := connectWithProvider(context.Background(), fakeProvider{openErr: wantErr}, testOptions())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapped %v", err, wantErr)
	}
}
