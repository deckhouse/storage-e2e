package storage_e2e

import (
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

func Setup() error {
	if err := config.ValidateEnvironment(); err != nil {
		return err
	}

	return logger.Initialize()
}
