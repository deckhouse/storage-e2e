/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package setup

import (
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// Init validates environment and initializes the logger.
// Must be called from test BeforeSuite when using cluster or other storage-e2e packages.
func Init() error {
	if err := config.ValidateEnvironment(); err != nil {
		return err
	}
	return logger.Initialize()
}

// Close closes the logger and any open log files.
// Should be called from test AfterSuite.
func Close() error {
	return logger.Close()
}
