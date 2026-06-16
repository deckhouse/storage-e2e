package main

import (
	"context"

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
		slogger.Error("failed to get provider", registryGetErr)
		return
	}

	clusterProvider, err := newProvider(slogger, cfg)
	if err != nil {
		slogger.Error("failed to build provider", err)
		return
	}

	teardownErr := clusterProvider.Remove(context.TODO())
	if teardownErr != nil {
		slogger.Error("failed to tear down cluster", teardownErr)
		return
	}
}
