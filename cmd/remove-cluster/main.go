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
		log.Fatal("failed to initialize config", "error", err)
	}

	newProvider, registryGetErr := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatal("failed to get provider", "error", registryGetErr)
	}

	slogger := logger.GetLogger()
	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		log.Fatal("failed to build provider", "error", err)
	}

	teardownErr := clusterProvider.Remove(context.Background())
	if teardownErr != nil {
		log.Fatal("failed to tear down cluster", "error", teardownErr)
	}
}
