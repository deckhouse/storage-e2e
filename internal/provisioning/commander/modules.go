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
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const (
	// moduleConfigGroupVersion is the Deckhouse API group/version that serves
	// ModuleConfig. A freshly created cluster can report Ready in Commander before
	// Deckhouse finishes registering it, so we wait for it before enabling modules.
	moduleConfigGroupVersion = "deckhouse.io/v1alpha1"

	// moduleConfigAPITimeout bounds the wait for the ModuleConfig API to appear.
	moduleConfigAPITimeout = 15 * time.Minute
)

// enableModules connects to the freshly created cluster and enables/configures
// the modules declared in cluster_config (ModuleConfig + ModulePullOverride),
// waiting for them to become Ready. It folds what used to be a separate
// enable-modules pipeline step into Bootstrap, so the modules-under-test come up
// as part of provisioning (mirroring how the DVP flow brings modules up with the
// cluster) and no post-bootstrap step is required.
func (p *commanderProvider) enableModules(ctx context.Context) error {
	clusterDef, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return fmt.Errorf("load cluster definition %q: %w", p.cfg.ClusterBootstrapConfigPath, err)
	}
	p.logger.Info("enabling modules from cluster config",
		"path", p.cfg.ClusterBootstrapConfigPath,
		"modules", len(clusterDef.DKPParameters.Modules),
	)

	creds, err := p.conf.Resolve()
	if err != nil {
		return fmt.Errorf("resolve commander credentials for module enablement: %w", err)
	}

	restConfig, cleanup, err := newConnector(p.client, p.conf, creds, p.logger).Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to cluster for module enablement: %w", err)
	}
	defer cleanup()

	// The cluster can be Ready in Commander before Deckhouse has registered its
	// ModuleConfig API; wait for it (also absorbs a transient tunnel EOF).
	p.logger.Info("waiting for the Deckhouse ModuleConfig API to be served")
	if err := waitForModuleConfigAPI(ctx, restConfig, moduleConfigAPITimeout); err != nil {
		return err
	}

	// sshClient is nil: EnableAndConfigureModules drives everything through the
	// Kubernetes API (ModuleConfig / ModulePullOverride) and does not use SSH.
	if err := kubernetes.EnableAndConfigureModules(ctx, restConfig, clusterDef, nil); err != nil {
		return fmt.Errorf("enable and configure modules: %w", err)
	}
	p.logger.Info("all modules enabled, configured and Ready")
	return nil
}

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
			return fmt.Errorf("timeout waiting for the Deckhouse ModuleConfig API: %s", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
