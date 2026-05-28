package storage_e2e

import (
	"fmt"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

func Initialize() error {
	if err := logger.Initialize(); err != nil {
		return fmt.Errorf("logger initialization: %w", err)
	}
	if err := config.ValidateEnvironment(); err != nil {
		return fmt.Errorf("environment validation: %w", err)
	}
	return nil
}
