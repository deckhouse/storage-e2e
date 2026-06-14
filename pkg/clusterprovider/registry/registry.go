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
	"fmt"
	"log/slog"
	"sync"

	"github.com/deckhouse/storage-e2e/internal/provisioning/dvp"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

// DefaultRegistry is the package-level registry strategies self-register into.
var DefaultRegistry = NewRegistry()

// Constructor builds a Provider, loading its own strategy-specific env. It runs
// lazily — only for the strategy selected at runtime — so unselected strategies
// never read their env. Validate on the resulting Provider stays a pure check
// (no loading, no I/O).
type Constructor func(logger *slog.Logger, config *clusterprovider.ClusterConfig) (clusterprovider.Provider, error)

// Registry maps strategy names to their Constructor.
type Registry struct {
	mu           sync.RWMutex
	constructors map[string]Constructor
}

// NewRegistry returns a Registry.
func NewRegistry() *Registry {
	return &Registry{constructors: map[string]Constructor{
		clusterprovider.ModeDVP: dvp.NewDVPProvider,
	}}
}

// Register adds a Constructor under name. A later registration with the same
// name overwrites the earlier one.
func (r *Registry) Register(name string, c Constructor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.constructors[name] = c
}

// Get returns the Constructor registered under name.
func (r *Registry) Get(name clusterprovider.ProviderMode) (Constructor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.constructors[name.String()]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", name)
	}
	return c, nil
}
