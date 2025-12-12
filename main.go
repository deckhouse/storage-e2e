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

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func main() {
	// config.ParseFlags() // TODO - investigate flag parsing with go test later.

	// Validate that at least one of the flags is set
	if (!config.AlwaysUseExisting() && !config.AlwaysCreateNew()) || (config.AlwaysUseExisting() && config.AlwaysCreateNew()) {
		fmt.Fprintf(os.Stderr, "Error: Either --always-use-existing or --always-create-new must be set, but not both\n\n")
		flag.Usage()
		os.Exit(1)
	}
}
