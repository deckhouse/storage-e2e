/*
Copyright 2026 Flant JSC

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

// Package commander exposes suite-facing operations against the Deckhouse
// Commander that provisioned the test cluster. Unlike attaching to the cluster
// (which the e2e SDK / legacy pkg/cluster handle), these operate on the cluster
// definition in Commander itself — e.g. resizing the control plane — and are
// env-driven, so a suite can call them regardless of how it connected.
package commander

import (
	"context"

	commanderprov "github.com/deckhouse/storage-e2e/internal/provisioning/commander"
)

// SetMasterCount changes the number of control-plane nodes of the Commander-
// managed test cluster to masterCount (1 or 3) and blocks until the cluster has
// converged (Commander status in_sync).
//
// It reuses the same E2E_COMMANDER_* environment the provider used to
// bootstrap/connect (URL, token, cluster name, wait timeout), edits the cluster
// template's masterCount input value, and approves the disruptive control-plane
// change request Commander raises so the resize can proceed. Use it to exercise
// master-count transitions (e.g. 3->1->3) against a live cluster; setting the
// count to its current value converges immediately.
//
// Only the Commander provider supports this: on other providers (e.g. dvp) the
// required E2E_COMMANDER_* environment is absent and the call returns an error.
func SetMasterCount(ctx context.Context, masterCount int) error {
	return commanderprov.SetMasterCount(ctx, masterCount)
}
