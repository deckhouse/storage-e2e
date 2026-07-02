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

	// Headroom over the longest provider wait (commander E2E_COMMANDER_WAIT_TIMEOUT,
	// which may be raised to ~60m for slow cluster provisioning).
	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute*75)
	bootstrapErr := clusterProvider.Bootstrap(bootstrapCtx)
	bootstrapCancel()
	if bootstrapErr != nil {
		log.Fatalf("failed to bootstrap cluster: %v", bootstrapErr)
	}
}
