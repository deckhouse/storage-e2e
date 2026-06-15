// Command remove-cluster is the CI-only entrypoint that tears a cluster down.
// It is a thin wrapper: load ClusterConfig, resolve the strategy's Constructor
// from the provider Registry, build the Provider, then run the idempotent
// Remove (which derives target resources from config, not from bootstrap
// artifacts).
package main

import (
	"context"
	"log"

	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/registry"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

func main() {
	cfg, err := clusterprovider.NewClusterConfig()
	if err != nil {
		log.Fatal("failed to initialize config - ", err)
	}

	newProvider, registryGetErr := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatal("failed to get provider", registryGetErr)
	}

	slogger := logger.GetLogger()
	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		log.Fatal("failed to build provider", err)
	}

	teardownErr := clusterProvider.Remove(context.TODO())
	if teardownErr != nil {
		log.Fatal("failed to tear down cluster", teardownErr)
	}
}
