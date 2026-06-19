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
		log.Fatal("failed to initialize config", "err", err)
	}

	newProvider, registryGetErr := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatal("failed to get provider", registryGetErr)
	}
	slogger := logger.GetLogger()
	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		log.Fatal("failed to build provider", "err", err)
	}

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute*45)
	defer bootstrapCancel()

	bootstrapErr := clusterProvider.Bootstrap(bootstrapCtx)
	if bootstrapErr != nil {
		log.Fatal("failed to bootstrap cluster", "err", bootstrapErr)
	}
}
