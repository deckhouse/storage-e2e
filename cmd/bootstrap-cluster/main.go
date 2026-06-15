// Command bootstrap-cluster is the CI-only entrypoint that provisions a
// cluster. It is a thin wrapper: load ClusterConfig, resolve the strategy's
// Constructor from the provider Registry, build the Provider, then Bootstrap.
package main

import (
	"context"
	"log"
	"time"

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

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute*45)
	defer bootstrapCancel()
	bootstrapErr := clusterProvider.Bootstrap(bootstrapCtx)
	if bootstrapErr != nil {
		log.Fatal("failed to bootstrap cluster", bootstrapErr)
	}
}
