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

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/rest"
)

// Webhook retry configuration
const (
	// WebhookRetryAttempts is the number of retry attempts for webhook connection errors
	WebhookRetryAttempts = 10
	// WebhookRetryInitialDelay is the initial delay before first retry
	WebhookRetryInitialDelay = 3 * time.Second
	// WebhookRetryBackoffMultiplier is the multiplier for exponential backoff
	WebhookRetryBackoffMultiplier = 1.5
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

	// Retry logic for webhook connection errors
	maxRetries := WebhookRetryAttempts
	retryDelay := WebhookRetryInitialDelay
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check if ModuleConfig exists
		_, err := deckhouse.GetModuleConfig(ctx, kubeconfig, moduleConfig.Name)
		if err != nil {
			// Resource doesn't exist, create it
			err = deckhouse.CreateModuleConfig(ctx, kubeconfig, moduleConfig.Name, moduleConfig.Version, moduleConfig.Enabled, settings)
			if err != nil {
				lastErr = err
				// Check if it's a webhook connection error
				if isWebhookConnectionError(err) {
					if attempt < maxRetries-1 {
						// Wait before retrying
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(retryDelay):
							// Exponential backoff
							retryDelay = time.Duration(float64(retryDelay) * WebhookRetryBackoffMultiplier)
							continue
						}
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
				// Check if it's a webhook connection error
				if isWebhookConnectionError(err) {
					if attempt < maxRetries-1 {
						// Wait before retrying
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(retryDelay):
							// Exponential backoff
							retryDelay = time.Duration(float64(retryDelay) * WebhookRetryBackoffMultiplier)
							continue
						}
					}
				}
				return fmt.Errorf("failed to update moduleconfig %s: %w", moduleConfig.Name, err)
			}
			return nil
		}
	}

	return fmt.Errorf("failed to configure moduleconfig %s after %d attempts: %w", moduleConfig.Name, maxRetries, lastErr)
}

// configureModuleConfigViaSSH creates or updates a ModuleConfig resource via kubectl over SSH
// This ensures the webhook is called from within the cluster network
// It retries on webhook connection errors to handle cases where the webhook service isn't ready yet
func configureModuleConfigViaSSH(ctx context.Context, sshClient ssh.SSHClient, moduleConfig *config.ModuleConfig) error {
	// Build ModuleConfig YAML
	moduleConfigYAML := struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Version  int                    `yaml:"version"`
			Enabled  *bool                  `yaml:"enabled"`
			Settings map[string]interface{} `yaml:"settings,omitempty"`
		} `yaml:"spec"`
	}{
		APIVersion: "deckhouse.io/v1alpha1",
		Kind:       "ModuleConfig",
		Metadata: struct {
			Name string `yaml:"name"`
		}{
			Name: moduleConfig.Name,
		},
		Spec: struct {
			Version  int                    `yaml:"version"`
			Enabled  *bool                  `yaml:"enabled"`
			Settings map[string]interface{} `yaml:"settings,omitempty"`
		}{
			Version:  moduleConfig.Version,
			Enabled:  &moduleConfig.Enabled,
			Settings: moduleConfig.Settings, // nil or empty map will be omitted due to omitempty
		},
	}

	yamlBytes, err := yaml.Marshal(moduleConfigYAML)
	if err != nil {
		return fmt.Errorf("failed to marshal ModuleConfig YAML: %w", err)
	}

	cmd := fmt.Sprintf("sudo /opt/deckhouse/bin/kubectl apply -f - << 'MODULECONFIG_EOF'\n%sMODULECONFIG_EOF", string(yamlBytes))
	if err := execWithWebhookRetry(ctx, sshClient, cmd, moduleConfig.Name); err != nil {
		return fmt.Errorf("failed to apply ModuleConfig %s via SSH: %w", moduleConfig.Name, err)
	}

	return nil
}

// configureModulePullOverrideViaSSH creates or updates a ModulePullOverride resource via kubectl over SSH
func configureModulePullOverrideViaSSH(ctx context.Context, sshClient ssh.SSHClient, moduleConfig *config.ModuleConfig, registryRepo string) error {
	// Determine ModulePullOverride imageTag
	var imageTag string
	shouldCreateMPO := false

	if strings.HasPrefix(registryRepo, "dev-") {
		shouldCreateMPO = true
		if moduleConfig.ModulePullOverride != "" {
			imageTag = moduleConfig.ModulePullOverride
		} else {
			imageTag = "main"
		}
	} else {
		shouldCreateMPO = false
	}

	if !shouldCreateMPO {
		return nil
	}

	// Build ModulePullOverride YAML
	modulePullOverrideYAML := struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			ImageTag     string `yaml:"imageTag"`
			ScanInterval string `yaml:"scanInterval"`
			Rollback     bool   `yaml:"rollback"`
		} `yaml:"spec"`
	}{
		APIVersion: "deckhouse.io/v1alpha2",
		Kind:       "ModulePullOverride",
		Metadata: struct {
			Name string `yaml:"name"`
		}{
			Name: moduleConfig.Name,
		},
		Spec: struct {
			ImageTag     string `yaml:"imageTag"`
			ScanInterval string `yaml:"scanInterval"`
			Rollback     bool   `yaml:"rollback"`
		}{
			ImageTag:     imageTag,
			ScanInterval: "1m",
			Rollback:     false,
		},
	}

	yamlBytes, err := yaml.Marshal(modulePullOverrideYAML)
	if err != nil {
		return fmt.Errorf("failed to marshal ModulePullOverride YAML: %w", err)
	}

	cmd := fmt.Sprintf("sudo /opt/deckhouse/bin/kubectl apply -f - << 'MODULEPULLOVERRIDE_EOF'\n%sMODULEPULLOVERRIDE_EOF", string(yamlBytes))
	if err := execWithWebhookRetry(ctx, sshClient, cmd, moduleConfig.Name); err != nil {
		return fmt.Errorf("failed to apply ModulePullOverride %s via SSH: %w", moduleConfig.Name, err)
	}

	return nil
}

// execWithWebhookRetry executes a kubectl command via SSH with retry logic for webhook errors
func execWithWebhookRetry(ctx context.Context, sshClient ssh.SSHClient, cmd, resourceName string) error {
	maxRetries := WebhookRetryAttempts
	retryDelay := WebhookRetryInitialDelay

	var lastOutput string
	for attempt := 0; attempt < maxRetries; attempt++ {
		output, err := sshClient.Exec(ctx, cmd)
		if err == nil {
			return nil
		}
		lastOutput = output

		// Check if it's a webhook connection error (check both error and output)
		combinedErr := fmt.Sprintf("%v %s", err, output)
		if isWebhookConnectionError(fmt.Errorf("%s", combinedErr)) {
			if attempt < maxRetries-1 {
				fmt.Printf("    ⏳ Webhook not ready for %s, retrying in %v (attempt %d/%d)...\n",
					resourceName, retryDelay, attempt+1, maxRetries)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay):
					retryDelay = time.Duration(float64(retryDelay) * WebhookRetryBackoffMultiplier)
					continue
				}
			}
		}
		return fmt.Errorf("command failed: %w\nOutput: %s", err, output)
	}

	return fmt.Errorf("command failed after %d attempts\nLast output: %s", maxRetries, lastOutput)
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

	// Create or update ModulePullOverride if needed
	if shouldCreateMPO {
		_, err := deckhouse.GetModulePullOverride(ctx, kubeconfig, moduleConfig.Name)
		if err != nil {
			// Resource doesn't exist, create it
			if err := deckhouse.CreateModulePullOverride(ctx, kubeconfig, moduleConfig.Name, imageTag); err != nil {
				return fmt.Errorf("failed to create module pull override for %s: %w", moduleConfig.Name, err)
			}
		} else {
			// Resource exists, update it
			if err := deckhouse.UpdateModulePullOverride(ctx, kubeconfig, moduleConfig.Name, imageTag); err != nil {
				return fmt.Errorf("failed to update module pull override for %s: %w", moduleConfig.Name, err)
			}
		}
	}

	return nil
}

// EnableAndConfigureModules enables and configures modules based on cluster definition
// It builds a dependency graph and processes modules level by level using topological sort
// If sshClient is provided, it uses kubectl via SSH (recommended for webhook access from within cluster)
// Otherwise, it falls back to using kubeconfig directly
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
	for levelIndex, level := range levels {
		for _, moduleConfig := range level {
			// Configure ModuleConfig
			if sshClient != nil {
				if err := configureModuleConfigViaSSH(ctx, sshClient, moduleConfig); err != nil {
					return err
				}
			} else {
				if err := configureModuleConfig(ctx, kubeconfig, moduleConfig); err != nil {
					return err
				}
			}

			// Configure ModulePullOverride
			if sshClient != nil {
				if err := configureModulePullOverrideViaSSH(ctx, sshClient, moduleConfig, clusterDef.DKPParameters.RegistryRepo); err != nil {
					return err
				}
			} else {
				if err := configureModulePullOverride(ctx, kubeconfig, moduleConfig, clusterDef.DKPParameters.RegistryRepo); err != nil {
					return err
				}
			}
		}
		// All modules at this level are now configured
		// Next level modules can be processed as their dependencies are satisfied
		_ = levelIndex // Can be used for logging if needed
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

			// Continue waiting even if module is in Error phase - it may recover
		}
	}
}
