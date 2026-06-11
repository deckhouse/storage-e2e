// Command bootstrap-cluster is the CI-only entrypoint that provisions a
// cluster. It is a thin wrapper: load ClusterConfig, resolve the strategy's
// Constructor from the provider Registry, build the Provider, then Bootstrap.
package main

import (
	"context"
	"log"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/provider"
)

func main() {
	cfg, err := config.New()
	if err != nil {
		log.Fatal("failed to initialize config - ", err)
	}

	newProvider, registryGetErr := provider.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatal("failed to get provider", registryGetErr)
	}

	clusterProvider, err := newProvider(cfg)
	if err != nil {
		log.Fatal("failed to build provider", err)
	}

	bootstrapErr := clusterProvider.Bootstrap(context.Background())
	if bootstrapErr != nil {
		log.Fatal("failed to bootstrap cluster", bootstrapErr)
	}
}
