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

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute*45)
	bootstrapErr := clusterProvider.Bootstrap(bootstrapCtx)
	bootstrapCancel()
	if bootstrapErr != nil {
		log.Fatalf("failed to bootstrap cluster: %v", bootstrapErr)
	}
}
