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

// Command enable-modules is the e2e pipeline's module-enablement step. It runs
// AFTER the cluster is bootstrapped and BEFORE the tests: given the cluster YAML
// (E2E_CLUSTER_CONFIG_YAML_PATH), it enables and configures the modules declared
// under dkpParameters.modules (ModuleConfig + ModulePullOverride) and waits for
// them to become Ready.
//
// It connects to the Commander-created cluster the same way the test suite does:
// through the commander connector, which resolves the master over SSH (via the
// bastion), fetches the kubeconfig off the master and opens an in-process API
// tunnel — no kubeconfig artifact and no external SSH tunnel are needed.
// modulePullOverride values may reference environment variables as ${NAME}
// (resolved by LoadClusterDefinition), which is how a per-PR image tag is
// injected, e.g. `modulePullOverride: "${SDS_OBJECT_IMAGE_TAG}"`.
package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/internal/provisioning/commander"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

// moduleConfigGroupVersion is the Deckhouse API group/version that serves
// ModuleConfig. A freshly bootstrapped cluster can report Ready in Commander
// before Deckhouse finishes registering it, so we wait for it before enabling
// modules.
const moduleConfigGroupVersion = "deckhouse.io/v1alpha1"

// waitForModuleConfigAPI polls discovery until deckhouse.io/v1alpha1 serves
// moduleconfigs, tolerating transient errors (e.g. a connection EOF over a
// freshly opened SSH tunnel).
func waitForModuleConfigAPI(ctx context.Context, restConfig *rest.Config, timeout time.Duration) error {
	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var last string
	for {
		list, err := dc.ServerResourcesForGroupVersion(moduleConfigGroupVersion)
		if err == nil {
			for _, r := range list.APIResources {
				if r.Name == "moduleconfigs" {
					return nil
				}
			}
			last = "group present but moduleconfigs resource not listed yet"
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return &waitTimeoutError{last}
		}
		log.Printf("waiting for the Deckhouse ModuleConfig API (%s) to be served; last: %s", moduleConfigGroupVersion, last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

type waitTimeoutError struct{ last string }

func (e *waitTimeoutError) Error() string {
	return "timeout waiting for the Deckhouse ModuleConfig API: " + e.last
}

func envMap() map[string]string {
	environ := os.Environ()
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func main() {
	clusterConfigPath := os.Getenv("E2E_CLUSTER_CONFIG_YAML_PATH")
	if clusterConfigPath == "" {
		log.Fatalf("E2E_CLUSTER_CONFIG_YAML_PATH is required (path to the cluster YAML)")
	}

	slogger := logger.GetLogger()

	clusterDef, err := config.LoadClusterDefinition(clusterConfigPath)
	if err != nil {
		log.Fatalf("failed to load cluster definition from %q: %v", clusterConfigPath, err)
	}
	slogger.Info("loaded cluster definition",
		"path", clusterConfigPath,
		"modules", len(clusterDef.DKPParameters.Modules),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// Connect to the Commander cluster in-process (SSH to master via bastion,
	// fetch kubeconfig, open API tunnel). cleanup tears the tunnel down.
	slogger.Info("connecting to the Commander cluster")
	restConfig, cleanup, err := commander.Connect(ctx, envMap(), slogger)
	if err != nil {
		log.Fatalf("failed to connect to the Commander cluster: %v", err)
	}
	defer cleanup()

	// The cluster can be Ready in Commander before Deckhouse has registered its
	// ModuleConfig API; wait for it (also absorbs a transient tunnel EOF).
	slogger.Info("waiting for the Deckhouse ModuleConfig API to be served")
	if err := waitForModuleConfigAPI(ctx, restConfig, 15*time.Minute); err != nil {
		log.Fatalf("Deckhouse ModuleConfig API not available: %v", err)
	}
	slogger.Info("Deckhouse ModuleConfig API is available")

	// sshClient is nil: EnableAndConfigureModules drives everything through the
	// Kubernetes API (ModuleConfig / ModulePullOverride) and does not use SSH.
	if err := kubernetes.EnableAndConfigureModules(ctx, restConfig, clusterDef, nil); err != nil {
		log.Fatalf("failed to enable and configure modules: %v", err)
	}
	slogger.Info("all modules enabled, configured and Ready")
}
