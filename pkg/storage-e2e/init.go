package storage_e2e

import (
	"github.com/deckhouse/storage-e2e/internal/config"
)

func Init() error {
	return config.ValidateEnvironment()
}
