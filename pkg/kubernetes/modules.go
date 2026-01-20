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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/internal/logger"
	"k8s.io/client-go/rest"
)

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

	// Retry logic for webhook connection errors and network timeouts
	maxRetries := 10
	retryDelay := 2 * time.Second
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
				if (isWebhookConnectionError(err) || isRetryableNetworkError(err)) && attempt < maxRetries-1 {
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
						// Exponential backoff
						retryDelay = time.Duration(float64(retryDelay) * 1.5)
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
				if (isWebhookConnectionError(err) || isRetryableNetworkError(err)) && attempt < maxRetries-1 {
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
						// Exponential backoff
						retryDelay = time.Duration(float64(retryDelay) * 1.5)
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
					if isRetryableNetworkError(err) && attempt < maxRetries-1 {
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
					if isRetryableNetworkError(err) && attempt < maxRetries-1 {
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

// isRetryableNetworkError checks if an error is a network error that should be retried
func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Check for common retryable network errors
	return strings.Contains(errStr, "TLS handshake timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "broken pipe")
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

// WaitForModuleReady waits for a module to reach the Ready phase
// It continues waiting even if the module is temporarily in Error phase, as modules can recover.
// Only fails if the timeout is exceeded and the module is still not Ready.
func WaitForModuleReady(ctx context.Context, kubeconfig *rest.Config, moduleName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			module, err := deckhouse.GetModule(ctx, kubeconfig, moduleName)
			if err != nil {
				// Module doesn't exist yet, continue waiting
				continue
			}

			if module.Status.Phase == "Ready" {
				return nil
			}

			// Check timeout only after checking the phase
			// This ensures we wait the full timeout period even if module is in Error phase
			if time.Now().After(deadline) {
				if module.Status.Phase == "Error" {
					return fmt.Errorf("timeout waiting for module %s to be ready: module is still in Error phase after %v", moduleName, timeout)
				}
				return fmt.Errorf("timeout waiting for module %s to be ready: module is in %s phase after %v", moduleName, module.Status.Phase, timeout)
			}
		}
	}
}
