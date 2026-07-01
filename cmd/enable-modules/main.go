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
// AFTER the cluster is bootstrapped and BEFORE the tests: given a kubeconfig
// (KUBE_CONFIG_PATH) and the cluster YAML (E2E_CLUSTER_CONFIG_YAML_PATH), it
// enables and configures the modules declared under dkpParameters.modules
// (ModuleConfig + ModulePullOverride) and waits for them to become Ready.
//
// It connects to the target cluster directly from the kubeconfig file — it does
// NOT open an SSH tunnel — so the cluster API must be reachable from the runner
// (e.g. a kubeconfig exported by the Commander provider, see
// E2E_COMMANDER_KUBECONFIG_OUT). modulePullOverride values may reference
// environment variables as ${NAME} (resolved by LoadClusterDefinition), which is
// how a per-PR image tag is injected, e.g.
// `modulePullOverride: "${SDS_OBJECT_IMAGE_TAG}"`.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
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

func main() {
	kubeconfigPath := os.Getenv("KUBE_CONFIG_PATH")
	if kubeconfigPath == "" {
		log.Fatalf("KUBE_CONFIG_PATH is required (path to the target cluster kubeconfig)")
	}
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

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatalf("failed to build rest config from kubeconfig %q: %v", kubeconfigPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

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
