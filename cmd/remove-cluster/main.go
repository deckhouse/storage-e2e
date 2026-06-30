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
		log.Fatalf("failed to initialize config: %v", err)
	}

	newProvider, registryGetErr := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatalf("failed to get provider: %v", registryGetErr)
	}

	slogger := logger.GetLogger()
	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		log.Fatalf("failed to build provider: %v", err)
	}

	teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	teardownErr := clusterProvider.Remove(teardownCtx)
	teardownCancel()
	if teardownErr != nil {
		log.Fatalf("failed to tear down cluster: %v", teardownErr)
	}
}
