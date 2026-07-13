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

package commander

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
)

// masterCountInputKey is the Commander template input value that controls the
// number of control-plane nodes (the template's `.masterCount`).
const masterCountInputKey = "masterCount"

// SetMasterCount changes the number of control-plane nodes of the Commander
// cluster named by E2E_COMMANDER_CLUSTER_NAME to masterCount (1 or 3) and waits
// for the cluster to converge (status in_sync).
//
// It is env-driven so a running test suite can call it without holding the
// provider handle: it reads E2E_COMMANDER_URL/TOKEN/CLUSTER_NAME (and the auth
// tuning) exactly like the provider, updates the template input value, approves
// the disruptive control-plane change request Commander raises, and waits within
// E2E_COMMANDER_WAIT_TIMEOUT.
func SetMasterCount(ctx context.Context, masterCount int) error {
	if masterCount != 1 && masterCount != 3 {
		return fmt.Errorf("masterCount must be 1 or 3, got %d", masterCount)
	}

	// Parse only the API-facing config (URL/Token/ClusterName/WaitTimeout); the
	// SSH fields are not needed for an API update, so Validate() is not called.
	conf := &Config{}
	if err := env.Parse(conf); err != nil {
		return fmt.Errorf("parse commander config: %w", err)
	}
	if conf.ClusterName == "" {
		return fmt.Errorf("E2E_COMMANDER_CLUSTER_NAME is required to set the master count")
	}

	client, err := newAPIClient(conf)
	if err != nil {
		return err
	}

	timeout := conf.WaitTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	// The template's masterCount input is a string enum ("1"/"3"), so send the
	// value as a string — a JSON number is rejected with a 422 enum mismatch.
	return client.SetClusterInputValueAndWait(ctx, conf.ClusterName, masterCountInputKey, strconv.Itoa(masterCount), timeout)
}
