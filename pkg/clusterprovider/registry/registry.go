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

// Package registry maps provider modes to their constructors and exposes a
// default registry pre-populated with the built-in providers.
package registry

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/deckhouse/storage-e2e/internal/provisioning/commander"
	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

// DefaultRegistry is the process-wide registry seeded with the built-in providers.
var DefaultRegistry = NewRegistry()

// Constructor builds a Provider for a given logger and cluster configuration.
type Constructor func(logger *slog.Logger, config *clusterprovider.ClusterConfig) (clusterprovider.Provider, error)

// Registry is a concurrency-safe mapping from provider mode to Constructor.
type Registry struct {
	mu           sync.RWMutex
	constructors map[string]Constructor
}

// NewRegistry returns a Registry pre-populated with the built-in providers.
func NewRegistry() *Registry {
	return &Registry{constructors: map[string]Constructor{
		clusterprovider.ModeDVP:       dvp.NewDVPProvider,
		clusterprovider.ModeCommander: commander.NewCommanderProvider,
	}}
}

// Register adds or replaces the constructor registered under name.
func (r *Registry) Register(name string, c Constructor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.constructors[name] = c
}

// Get returns the constructor registered for the given provider mode, or an
// error if no provider is registered under that mode.
func (r *Registry) Get(name clusterprovider.ProviderMode) (Constructor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.constructors[name.String()]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", name)
	}
	return c, nil
}
