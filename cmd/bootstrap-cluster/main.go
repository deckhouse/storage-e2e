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
	slogger := logger.GetLogger()

	cfg, err := clusterprovider.NewClusterConfig()
	if err != nil {
		slogger.Error("failed to initialize config - ", err)
		return
	}

	newProvider, registryGetErr := registry.DefaultRegistry.Get(cfg.ClusterProvider)
	if registryGetErr != nil {
		log.Fatal("failed to get provider", registryGetErr)
	}

	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		slogger.Error("failed to build provider", err)
		return
	}

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute*45)
	defer bootstrapCancel()

	bootstrapErr := clusterProvider.Bootstrap(bootstrapCtx)
	if bootstrapErr != nil {
		slogger.Error("failed to bootstrap cluster", bootstrapErr)
		return
	}
}
