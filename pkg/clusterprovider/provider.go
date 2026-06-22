/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package clusterprovider defines the provider abstraction used to bootstrap
// and tear down test clusters, along with the provider mode and env-based
// configuration shared by concrete provider implementations.
package clusterprovider

import (
	"context"
)

// Provider provisions and removes a test cluster for a specific backend
// (for example DVP). Implementations are expected to be idempotent.
type Provider interface {
	Name() string
	Bootstrap(ctx context.Context) error
	Remove(ctx context.Context) error
}
