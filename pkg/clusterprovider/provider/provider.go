// Package provider contains the CI-only provisioning strategies. It is used
// exclusively by the cmd/* binaries to bootstrap and tear down clusters; tests
// never import it.
//
// Strategies self-register a Constructor into a Registry via init (Open/Closed).
// Only two strategies exist: dvp and commander.
package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/config"
)

const (
	ModeDvp       = "dvp"
	ModeCommander = "commander"
)

// Provider is a provisioning strategy. Bootstrap returns Endpoints (not a
// Cluster); Teardown is idempotent and derives target resources from config.
type Provider interface {
	Name() string
	Bootstrap(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// Constructor builds a Provider, loading its own strategy-specific env. It runs
// lazily — only for the strategy selected at runtime — so unselected strategies
// never read their env. Validate on the resulting Provider stays a pure check
// (no loading, no I/O).
type Constructor func(config *config.ClusterConfig) (Provider, error)

// Registry maps strategy names to their Constructor.
type Registry struct {
	mu           sync.RWMutex
	constructors map[string]Constructor
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{constructors: make(map[string]Constructor)}
}

// DefaultRegistry is the package-level registry strategies self-register into.
var DefaultRegistry = NewRegistry()

// Register adds a Constructor under name. A later registration with the same
// name overwrites the earlier one.
func (r *Registry) Register(name string, c Constructor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.constructors[name] = c
}

// Get returns the Constructor registered under name.
func (r *Registry) Get(name string) (Constructor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.constructors[name]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", name)
	}
	return c, nil
}
