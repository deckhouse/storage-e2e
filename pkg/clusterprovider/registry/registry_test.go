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

package registry

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type fakeProvider struct {
	name string
}

func (p *fakeProvider) Name() string                      { return p.name }
func (p *fakeProvider) Bootstrap(_ context.Context) error { return nil }
func (p *fakeProvider) Remove(_ context.Context) error    { return nil }
func (p *fakeProvider) ConnectTestCluster(_ context.Context) (*clusterprovider.Cluster, error) {
	return nil, clusterprovider.ErrConnectUnsupported
}

func newFakeConstructor(name string, called *bool) Constructor {
	return func(_ *slog.Logger, _ *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
		if called != nil {
			*called = true
		}
		return &fakeProvider{name: name}, nil
	}
}

func TestNewRegistry_SeedsBuiltinProviders(t *testing.T) {
	r := NewRegistry()

	c, err := r.Get(clusterprovider.ModeDVP)
	if err != nil {
		t.Fatalf("Get(%q) returned unexpected error: %v", clusterprovider.ModeDVP, err)
	}
	if c == nil {
		t.Fatalf("Get(%q) returned nil constructor", clusterprovider.ModeDVP)
	}
}

func TestRegistryGet_UnregisteredMode(t *testing.T) {
	r := NewRegistry()

	const unknownMode = clusterprovider.ProviderMode("nonexistent")
	c, err := r.Get(unknownMode)
	if err == nil {
		t.Fatalf("Get(%q) expected error for unregistered mode, got nil", unknownMode)
	}
	if c != nil {
		t.Errorf("Get(%q) expected nil constructor on error, got %v", unknownMode, c)
	}
}

func TestRegistryRegister_AddsConstructor(t *testing.T) {
	r := NewRegistry()

	var called bool
	r.Register(clusterprovider.ModeCommander, newFakeConstructor("fake", &called))

	c, err := r.Get(clusterprovider.ModeCommander)
	if err != nil {
		t.Fatalf("Get(%q) returned unexpected error: %v", clusterprovider.ModeCommander, err)
	}

	provider, err := c(slog.Default(), &clusterprovider.ClusterConfig{})
	if err != nil {
		t.Fatalf("constructor returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected registered constructor to be invoked")
	}
	if got, want := provider.Name(), "fake"; got != want {
		t.Errorf("provider.Name() = %q, want %q", got, want)
	}
}

func TestRegistryRegister_ReplacesExistingConstructor(t *testing.T) {
	r := NewRegistry()

	var firstCalled, secondCalled bool
	r.Register(clusterprovider.ModeDVP, newFakeConstructor("first", &firstCalled))
	r.Register(clusterprovider.ModeDVP, newFakeConstructor("second", &secondCalled))

	c, err := r.Get(clusterprovider.ModeDVP)
	if err != nil {
		t.Fatalf("Get(%q) returned unexpected error: %v", clusterprovider.ModeDVP, err)
	}

	provider, err := c(slog.Default(), &clusterprovider.ClusterConfig{})
	if err != nil {
		t.Fatalf("constructor returned unexpected error: %v", err)
	}
	if firstCalled {
		t.Error("expected replaced constructor not to be invoked")
	}
	if !secondCalled {
		t.Error("expected replacement constructor to be invoked")
	}
	if got, want := provider.Name(), "second"; got != want {
		t.Errorf("provider.Name() = %q, want %q", got, want)
	}
}

func TestDefaultRegistry_HasBuiltinProviders(t *testing.T) {
	for _, mode := range []clusterprovider.ProviderMode{clusterprovider.ModeDVP, clusterprovider.ModeCommander} {
		c, err := DefaultRegistry.Get(mode)
		if err != nil {
			t.Fatalf("DefaultRegistry.Get(%q) returned unexpected error: %v", mode, err)
		}
		if c == nil {
			t.Fatalf("DefaultRegistry.Get(%q) returned nil constructor", mode)
		}
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			r.Register(clusterprovider.ModeCommander, newFakeConstructor("fake", nil))
		}()
		go func() {
			defer wg.Done()
			_, _ = r.Get(clusterprovider.ModeDVP)
		}()
	}

	wg.Wait()
}
