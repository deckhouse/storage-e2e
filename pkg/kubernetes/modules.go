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

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	deckhousev1alpha1 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

// ModuleSpec defines a module to be enabled in the cluster.
// This is a simplified version of config.ModuleConfig that provides
// a clean API for test writers.
type ModuleSpec struct {
	// Name is the name of the module (e.g., "snapshot-controller", "csi-hpe")
	Name string

	// Version is the module config version (typically 1)
	Version int

	// Enabled indicates whether the module should be enabled
	Enabled bool

	// Settings contains module-specific settings
	Settings map[string]interface{}

	// Dependencies lists module names that must be enabled before this one
	Dependencies []string

	// ModulePullOverride overrides the module pull branch/tag (e.g., "main", "pr123")
	// Only used for dev registries (registries starting with "dev-")
	ModulePullOverride string
}

// TestClusterResourcesInterface defines the interface for accessing test cluster resources
// This avoids circular imports with the cluster package
type TestClusterResourcesInterface interface {
	GetKubeconfig() *rest.Config
	GetSSHClient() ssh.SSHClient
	GetClusterDefinition() *config.ClusterDefinition
}

// convertModuleSpecsToConfigs converts ModuleSpec slice to config.ModuleConfig slice
func convertModuleSpecsToConfigs(modules []ModuleSpec) []*config.ModuleConfig {
	moduleConfigs := make([]*config.ModuleConfig, len(modules))
	for i, spec := range modules {
		settings := spec.Settings
		if settings == nil {
			settings = make(map[string]interface{})
		}
		moduleConfigs[i] = &config.ModuleConfig{
			Name:               spec.Name,
			Version:            spec.Version,
			Enabled:            spec.Enabled,
			Settings:           settings,
			Dependencies:       spec.Dependencies,
			ModulePullOverride: spec.ModulePullOverride,
		}
	}
	return moduleConfigs
}

// EnableModulesWithSpecs enables and configures the specified modules in the test cluster.
// It handles dependencies automatically through topological sort and waits for
// each level of modules to become Ready before proceeding to the next level.
func EnableModulesWithSpecs(ctx context.Context, kubeconfig *rest.Config, sshClient ssh.SSHClient, clusterDef *config.ClusterDefinition, modules []ModuleSpec) error {
	// Convert ModuleSpec to config.ModuleConfig
	moduleConfigs := convertModuleSpecsToConfigs(modules)

	// Get registry repo - from ClusterDefinition if available (new cluster mode),
	// otherwise use default value (existing cluster mode where ClusterDefinition is nil)
	registryRepo := "dev-registry.deckhouse.io/sys/deckhouse-oss"
	if clusterDef != nil {
		registryRepo = clusterDef.DKPParameters.RegistryRepo
	}

	// Create cluster definition with modules to enable
	effectiveClusterDef := &config.ClusterDefinition{
		DKPParameters: config.DKPParameters{
			Modules:      moduleConfigs,
			RegistryRepo: registryRepo,
		},
	}

	// Enable and configure modules
	return EnableAndConfigureModules(ctx, kubeconfig, effectiveClusterDef, sshClient)
}

// WaitForModulesReadyWithSpecs waits for the specified modules to become ready.
// This is typically called after EnableModulesWithSpecs to ensure all modules are operational.
//
// Parameters:
//   - ctx: context for cancellation
//   - kubeconfig: kubernetes client config
//   - clusterDef: cluster definition (can be nil for existing clusters)
//   - modules: list of module specifications to wait for
//   - timeout: maximum time to wait for all modules
func WaitForModulesReadyWithSpecs(ctx context.Context, kubeconfig *rest.Config, clusterDef *config.ClusterDefinition, modules []ModuleSpec, timeout time.Duration) error {
	// Convert ModuleSpec to config.ModuleConfig
	moduleConfigs := convertModuleSpecsToConfigs(modules)

	// Get registry repo
	registryRepo := "dev-registry.deckhouse.io/sys/deckhouse-oss"
	if clusterDef != nil {
		registryRepo = clusterDef.DKPParameters.RegistryRepo
	}

	// Create cluster definition with modules
	effectiveClusterDef := &config.ClusterDefinition{
		DKPParameters: config.DKPParameters{
			Modules:      moduleConfigs,
			RegistryRepo: registryRepo,
		},
	}

	return WaitForModulesReady(ctx, kubeconfig, effectiveClusterDef, timeout)
}

// EnableModulesAndWait is a convenience function that enables modules and waits
// for them to become ready in one call.
//
// Parameters:
//   - ctx: context for cancellation
//   - kubeconfig: kubernetes client config
//   - sshClient: SSH client for cluster access
//   - clusterDef: cluster definition (can be nil for existing clusters)
//   - modules: list of module specifications to enable
//   - timeout: maximum time to wait for all modules to become ready
func EnableModulesAndWait(ctx context.Context, kubeconfig *rest.Config, sshClient ssh.SSHClient, clusterDef *config.ClusterDefinition, modules []ModuleSpec, timeout time.Duration) error {
	if err := EnableModulesWithSpecs(ctx, kubeconfig, sshClient, clusterDef, modules); err != nil {
		return err
	}
	return WaitForModulesReadyWithSpecs(ctx, kubeconfig, clusterDef, modules, timeout)
}

// moduleGraph represents the dependency graph structure
type moduleGraph struct {
	modules      map[string]*config.ModuleConfig // module name -> module config
	dependencies map[string][]string             // module name -> list of dependency names
	reverseDeps  map[string][]string             // module name -> list of modules that depend on it
}

// buildModuleGraph builds a dependency graph from module configurations
func buildModuleGraph(modules []*config.ModuleConfig) (*moduleGraph, error) {
	graph := &moduleGraph{
		modules:      make(map[string]*config.ModuleConfig),
		dependencies: make(map[string][]string),
		reverseDeps:  make(map[string][]string),
	}

	// Build module map and dependency lists
	for _, module := range modules {
		graph.modules[module.Name] = module
		graph.dependencies[module.Name] = module.Dependencies

		// Build reverse dependencies (which modules depend on this one)
		for _, depName := range module.Dependencies {
			graph.reverseDeps[depName] = append(graph.reverseDeps[depName], module.Name)
		}
	}

	// Validate that all dependencies exist
	for _, module := range modules {
		for _, depName := range module.Dependencies {
			if _, exists := graph.modules[depName]; !exists {
				return nil, fmt.Errorf("dependency module %s not found for module %s", depName, module.Name)
			}
		}
	}

	return graph, nil
}

// topologicalSortLevels performs topological sort and returns modules organized by levels
// Level 0 contains modules with no dependencies, level 1 contains modules that only depend on level 0, etc.
func topologicalSortLevels(graph *moduleGraph) ([][]*config.ModuleConfig, error) {
	// Calculate in-degrees (number of unresolved dependencies)
	inDegree := make(map[string]int)
	for name := range graph.modules {
		inDegree[name] = len(graph.dependencies[name])
	}

	levels := [][]*config.ModuleConfig{}

	// Process levels until all modules are processed
	for len(inDegree) > 0 {
		// Find all modules with no remaining dependencies (current level)
		currentLevel := []*config.ModuleConfig{}
		for name, degree := range inDegree {
			if degree == 0 {
				currentLevel = append(currentLevel, graph.modules[name])
			}
		}

		// If no modules found with degree 0, there's a cycle
		if len(currentLevel) == 0 {
			remaining := []string{}
			for name := range inDegree {
				remaining = append(remaining, name)
			}
			return nil, fmt.Errorf("circular dependency detected among modules: %v", remaining)
		}

		// Add current level to result
		levels = append(levels, currentLevel)

		// Remove processed modules and update in-degrees of dependent modules
		for _, module := range currentLevel {
			delete(inDegree, module.Name)

			// Decrease in-degree for all modules that depend on this one
			for _, dependent := range graph.reverseDeps[module.Name] {
				if _, exists := inDegree[dependent]; exists {
					inDegree[dependent]--
				}
			}
		}
	}

	return levels, nil
}

// configureModuleConfig creates or updates a ModuleConfig resource
// It retries on webhook connection errors to handle cases where the webhook service isn't ready yet
func configureModuleConfig(ctx context.Context, kubeconfig *rest.Config, moduleConfig *config.ModuleConfig) error {
	settings := make(map[string]interface{})
	if moduleConfig.Settings != nil {
		settings = moduleConfig.Settings
	}

	// Retry logic for webhook connection errors and network timeouts.
	// On freshly-bootstrapped Deckhouse clusters the validating-webhook-handler
	// pod (or the d8-system Service endpoint backing it) can be unready for
	// several minutes while the control plane converges. Our previous cap of
	// 10 retries with exponential backoff topped out at ~3.7 minutes total
	// which was not enough for the SAN stand — we'd fail Step 18 with
	// "connection refused" during the first ModuleConfig write. Bumping to 60
	// attempts with delays capped at 30s gives us up to ~30 minutes of
	// soft-retries, which easily outlives any realistic webhook cold start.
	maxRetries := 60
	retryDelay := 2 * time.Second
	const maxRetryDelay = 30 * time.Second
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logger.Debug("Retrying ModuleConfig operation for %s (attempt %d/%d)",
				moduleConfig.Name, attempt+1, maxRetries)
		}

		// Check if ModuleConfig exists
		_, err := deckhouse.GetModuleConfig(ctx, kubeconfig, moduleConfig.Name)
		if err != nil {
			// Resource doesn't exist, create it
			err = deckhouse.CreateModuleConfig(ctx, kubeconfig, moduleConfig.Name, moduleConfig.Version, moduleConfig.Enabled, settings)
			if err != nil {
				lastErr = err
				// Check if it's a retryable error (webhook or network timeout)
				if retry.IsRetryable(err) && attempt < maxRetries-1 {
					if isWebhookConnectionError(err) {
						logger.Debug("webhook-handler connection error for %s", moduleConfig.Name)
					} else {
						logger.Warn("Network timeout error creating ModuleConfig for %s: %v", moduleConfig.Name, err)
					}
					// Wait before retrying
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryDelay):
						// Exponential backoff, capped so we don't sleep forever
						// between retries on a slow-to-converge cluster.
						retryDelay = time.Duration(float64(retryDelay) * 1.5)
						if retryDelay > maxRetryDelay {
							retryDelay = maxRetryDelay
						}
						continue
					}
				}
				return fmt.Errorf("failed to create moduleconfig %s: %w", moduleConfig.Name, err)
			}
			return nil
		} else {
			// Resource exists, update it
			err = deckhouse.UpdateModuleConfig(ctx, kubeconfig, moduleConfig.Name, moduleConfig.Version, moduleConfig.Enabled, settings)
			if err != nil {
				lastErr = err
				// Check if it's a retryable error (webhook or network timeout)
				if retry.IsRetryable(err) && attempt < maxRetries-1 {
					if isWebhookConnectionError(err) {
						logger.Debug("webhook-handler connection error for %s", moduleConfig.Name)
					} else {
						logger.Warn("Network timeout error updating ModuleConfig for %s: %v", moduleConfig.Name, err)
					}
					// Wait before retrying
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryDelay):
						// Exponential backoff, capped (see create branch above
						// for the rationale — same webhook cold-start).
						retryDelay = time.Duration(float64(retryDelay) * 1.5)
						if retryDelay > maxRetryDelay {
							retryDelay = maxRetryDelay
						}
						continue
					}
				}
				return fmt.Errorf("failed to update moduleconfig %s: %w", moduleConfig.Name, err)
			}
			return nil
		}
	}

	return fmt.Errorf("failed to configure moduleconfig %s after %d attempts: %w", moduleConfig.Name, maxRetries, lastErr)
}

// isWebhookConnectionError checks if the error is a webhook connection error
func isWebhookConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Check for common webhook connection error patterns
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "failed calling webhook") ||
		strings.Contains(errStr, "webhook") && strings.Contains(errStr, "timeout")
}

// configureModulePullOverride creates or updates a ModulePullOverride resource if needed
// Retries with exponential backoff on network/timeout errors
func configureModulePullOverride(ctx context.Context, kubeconfig *rest.Config, moduleConfig *config.ModuleConfig, registryRepo string) error {
	// Determine ModulePullOverride imageTag
	// If registryRepo starts with "dev-", always create MPO:
	//   - Use moduleConfig.ModulePullOverride if specified (not empty)
	//   - Otherwise use "main" as default
	// If registryRepo does NOT start with "dev-", we don't create MPO at all
	var imageTag string
	shouldCreateMPO := false

	if strings.HasPrefix(registryRepo, "dev-") {
		// Always create MPO for dev registries
		shouldCreateMPO = true
		if moduleConfig.ModulePullOverride != "" {
			imageTag = moduleConfig.ModulePullOverride
		} else {
			imageTag = "main"
		}
	} else {
		// Don't create MPO for non-dev registries
		shouldCreateMPO = false
	}

	// Create or update ModulePullOverride if needed with retry logic
	if shouldCreateMPO {
		maxRetries := 5
		retryDelay := 2 * time.Second
		var lastErr error

		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				logger.Debug("Retrying ModulePullOverride operation for %s (attempt %d/%d) after %v",
					moduleConfig.Name, attempt+1, maxRetries, retryDelay)
				// Wait before retry with exponential backoff
				select {
				case <-ctx.Done():
					return fmt.Errorf("context cancelled while retrying ModulePullOverride for %s: %w", moduleConfig.Name, ctx.Err())
				case <-time.After(retryDelay):
					// Exponential backoff: 2s, 4s, 8s, 16s
					retryDelay *= 2
				}
			}

			_, err := deckhouse.GetModulePullOverride(ctx, kubeconfig, moduleConfig.Name)
			if err != nil {
				// Resource doesn't exist, create it
				if err := deckhouse.CreateModulePullOverride(ctx, kubeconfig, moduleConfig.Name, imageTag); err != nil {
					lastErr = err
					// Check if it's a retryable error (timeout, TLS handshake, connection refused, etc.)
					if retry.IsRetryable(err) && attempt < maxRetries-1 {
						logger.Warn("Retryable error creating ModulePullOverride for %s: %v", moduleConfig.Name, err)
						continue
					}
					return fmt.Errorf("failed to create ModulePullOverride for %s: %w", moduleConfig.Name, err)
				}
				return nil // Success
			} else {
				// Resource exists, update it
				if err := deckhouse.UpdateModulePullOverride(ctx, kubeconfig, moduleConfig.Name, imageTag); err != nil {
					lastErr = err
					// Check if it's a retryable error
					if retry.IsRetryable(err) && attempt < maxRetries-1 {
						logger.Warn("Retryable error updating ModulePullOverride for %s: %v", moduleConfig.Name, err)
						continue
					}
					return fmt.Errorf("failed to update ModulePullOverride for %s: %w", moduleConfig.Name, err)
				}
				return nil // Success
			}
		}

		// If we exhausted all retries
		if lastErr != nil {
			return fmt.Errorf("failed to configure ModulePullOverride for %s after %d attempts: %w",
				moduleConfig.Name, maxRetries, lastErr)
		}
	}

	return nil
}

// EnableAndConfigureModules enables and configures modules based on cluster definition
// It builds a dependency graph and processes modules level by level using topological sort
// After configuring each level, it waits for all modules in that level to become Ready before proceeding to the next level
func EnableAndConfigureModules(ctx context.Context, kubeconfig *rest.Config, clusterDef *config.ClusterDefinition, sshClient ssh.SSHClient) error {
	if len(clusterDef.DKPParameters.Modules) == 0 {
		return nil
	}

	// Build dependency graph
	graph, err := buildModuleGraph(clusterDef.DKPParameters.Modules)
	if err != nil {
		return fmt.Errorf("failed to build module graph: %w", err)
	}

	// Perform topological sort to get modules organized by levels
	levels, err := topologicalSortLevels(graph)
	if err != nil {
		return fmt.Errorf("failed to sort modules: %w", err)
	}

	// Process modules level by level
	// Modules within each level are processed in parallel since they have no dependencies on each other
	for levelIndex, level := range levels {
		logger.Debug("Configuring module level %d with %d modules", levelIndex+1, len(level))

		// Configure all modules in this level in parallel
		var wg sync.WaitGroup
		errChan := make(chan error, len(level))

		for _, moduleConfig := range level {
			wg.Add(1)
			go func(mc *config.ModuleConfig) {
				defer wg.Done()

				logger.Debug("Enabling module %s", mc.Name)

				// Configure ModuleConfig
				if err := configureModuleConfig(ctx, kubeconfig, mc); err != nil {
					errChan <- fmt.Errorf("failed to create ModuleConfig for module %s: %w", mc.Name, err)
					return
				}

				// Configure ModulePullOverride
				if err := configureModulePullOverride(ctx, kubeconfig, mc, clusterDef.DKPParameters.RegistryRepo); err != nil {
					errChan <- fmt.Errorf("failed to create ModulePullOverride for module %s: %w", mc.Name, err)
					return
				}

				logger.Debug("Module %s configuration applied", mc.Name)
			}(moduleConfig)
		}

		// Wait for all configuration tasks to complete
		wg.Wait()
		close(errChan)

		// Check for configuration errors
		for err := range errChan {
			if err != nil {
				return err
			}
		}

		// Wait for all enabled modules in this level to become Ready before proceeding to next level
		logger.Debug("Waiting for modules in level %d to become Ready", levelIndex+1)

		// Reset channels for waiting phase
		errChan = make(chan error, len(level))

		for _, moduleConfig := range level {
			if moduleConfig.Enabled {
				wg.Add(1)
				go func(mc *config.ModuleConfig) {
					defer wg.Done()

					// Use ModuleDeployTimeout for each module
					if err := WaitForModuleReady(ctx, kubeconfig, mc.Name, config.ModuleDeployTimeout); err != nil {
						errChan <- fmt.Errorf("module %s in level %d failed to become ready: %w", mc.Name, levelIndex+1, err)
						return
					}
					logger.Debug("Module %s is Ready", mc.Name)
				}(moduleConfig)
			}
		}

		// Wait for all modules to become ready
		wg.Wait()
		close(errChan)

		// Check for readiness errors
		for err := range errChan {
			if err != nil {
				return err
			}
		}

		logger.Debug("All modules in level %d are Ready", levelIndex+1)
	}

	return nil
}

// WaitForModulesReady waits for all modules specified in cluster definition to be ready
// It builds a dependency graph and waits for modules level by level using topological sort
func WaitForModulesReady(ctx context.Context, kubeconfig *rest.Config, clusterDef *config.ClusterDefinition, timeout time.Duration) error {
	if len(clusterDef.DKPParameters.Modules) == 0 {
		return nil
	}

	// Build dependency graph
	graph, err := buildModuleGraph(clusterDef.DKPParameters.Modules)
	if err != nil {
		return fmt.Errorf("failed to build module graph: %w", err)
	}

	// Perform topological sort to get modules organized by levels
	levels, err := topologicalSortLevels(graph)
	if err != nil {
		return fmt.Errorf("failed to sort modules: %w", err)
	}

	// Wait for modules level by level
	for levelIndex, level := range levels {
		for _, moduleConfig := range level {
			// Only wait for enabled modules
			if moduleConfig.Enabled {
				if err := WaitForModuleReady(ctx, kubeconfig, moduleConfig.Name, timeout); err != nil {
					return fmt.Errorf("failed to wait for module %s to be ready: %w", moduleConfig.Name, err)
				}
			}
		}
		// All modules at this level are now ready
		// Next level modules can be waited for as their dependencies are satisfied
		_ = levelIndex // Can be used for logging if needed
	}

	return nil
}

// moduleReadyPollInterval is how often WaitForModuleReady re-reads the Module
// status while waiting for it to converge to the Ready phase.
const moduleReadyPollInterval = 2 * time.Second

// WaitForModuleReady polls a Deckhouse Module until it reaches the Ready phase.
//
// Reliability notes (this replaces an earlier one-shot phase check that was
// flaky during cluster creation):
//   - The whole wait is bounded by a derived context deadline, so persistent
//     GetModule failures (dropped SSH tunnel, API hiccup) also time out instead
//     of hanging until the parent context is canceled.
//   - Transient GetModule errors and intermediate phases (Downloading,
//     Installing, Reconciling, and even Error — modules can recover) are
//     tolerated; only the timeout is terminal.
//   - On timeout the error carries the last observed phase and the IsReady
//     condition message so a stuck module is diagnosable from logs alone.
func WaitForModuleReady(ctx context.Context, kubeconfig *rest.Config, moduleName string, timeout time.Duration) error {
	var lastPhase, lastCondition string

	// ready re-reads the module and reports whether it has converged, recording
	// the latest phase/condition for diagnostics on timeout.
	ready := func() bool {
		module, err := deckhouse.GetModule(ctx, kubeconfig, moduleName)
		if err != nil {
			// Module may not be registered yet, or the API/tunnel had a
			// transient failure. Keep waiting until the context deadline.
			lastPhase = ""
			logger.Debug("Waiting for module %s: not retrievable yet: %v", moduleName, err)
			return false
		}
		lastPhase = module.Status.Phase
		lastCondition = moduleReadyConditionMessage(module)
		return module.Status.Phase == deckhousev1alpha1.ModulePhaseReady
	}

	// Check once up front so an already-Ready module returns without paying the
	// first poll interval.
	if ready() {
		return nil
	}

	ticker := time.NewTicker(moduleReadyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Distinguish our derived deadline (a genuine module timeout) from a
			// canceled parent context (e.g. the test was aborted), so logs aren't
			// misleading about why the wait ended.
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("canceled while waiting for module %s to become ready (last phase %q%s): %w",
					moduleName, lastPhase, moduleConditionSuffix(lastCondition), ctx.Err())
			}
			return fmt.Errorf("timeout waiting for module %s to become ready after %v (last phase %q%s): %w",
				moduleName, timeout, lastPhase, moduleConditionSuffix(lastCondition), ctx.Err())
		case <-ticker.C:
			if ready() {
				return nil
			}
		}
	}
}

// moduleReadyConditionMessage extracts a human-readable summary of the module's
// IsReady condition for diagnostics. Returns "" when the condition is absent.
func moduleReadyConditionMessage(module *deckhouse.Module) string {
	for _, cond := range module.Status.Conditions {
		if cond.Type != deckhousev1alpha1.ModuleConditionIsReady {
			continue
		}
		if cond.Reason != "" || cond.Message != "" {
			return fmt.Sprintf("IsReady=%s reason=%q message=%q", cond.Status, cond.Reason, cond.Message)
		}
		return fmt.Sprintf("IsReady=%s", cond.Status)
	}
	return ""
}

// moduleConditionSuffix formats a condition summary as a ", <summary>" suffix,
// or "" when there is nothing to report.
func moduleConditionSuffix(condition string) string {
	if condition == "" {
		return ""
	}
	return ", " + condition
}
