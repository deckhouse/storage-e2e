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

package deckhouse

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deckhousev1alpha1 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha1"
	deckhousev1alpha2 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha2"
	"github.com/deckhouse/deckhouse/go_lib/libapi"
)

// GetModule retrieves detailed information about a single module by name
func GetModule(ctx context.Context, config *rest.Config, moduleName string) (*Module, error) {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	module := &deckhousev1alpha1.Module{}
	key := client.ObjectKey{Name: moduleName}
	if err := cl.client.Get(ctx, key, module); err != nil {
		return nil, fmt.Errorf("failed to get module %s: %w", moduleName, err)
	}

	return module, nil
}

// GetModuleConfig retrieves detailed information about a ModuleConfig by name
func GetModuleConfig(ctx context.Context, config *rest.Config, moduleName string) (*ModuleConfig, error) {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	moduleConfig := &deckhousev1alpha1.ModuleConfig{}
	key := client.ObjectKey{Name: moduleName}
	if err := cl.client.Get(ctx, key, moduleConfig); err != nil {
		return nil, fmt.Errorf("failed to get moduleconfig %s: %w", moduleName, err)
	}

	return moduleConfig, nil
}

// GetModulePullOverride retrieves detailed information about a ModulePullOverride by name
func GetModulePullOverride(ctx context.Context, config *rest.Config, moduleName string) (*ModulePullOverride, error) {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	modulePullOverride := &deckhousev1alpha2.ModulePullOverride{}
	key := client.ObjectKey{Name: moduleName}
	if err := cl.client.Get(ctx, key, modulePullOverride); err != nil {
		return nil, fmt.Errorf("failed to get modulepulloverride %s: %w", moduleName, err)
	}

	return modulePullOverride, nil
}

// CreateModuleConfig creates a new ModuleConfig resource
func CreateModuleConfig(ctx context.Context, config *rest.Config, moduleName string, version int, enabled bool, settings map[string]interface{}) error {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	moduleConfig := &deckhousev1alpha1.ModuleConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: moduleName,
		},
		Spec: deckhousev1alpha1.ModuleConfigSpec{
			Version:  version,
			Enabled:  &enabled,
			Settings: deckhousev1alpha1.SettingsValues(settings),
		},
	}

	if err := cl.client.Create(ctx, moduleConfig); err != nil {
		return fmt.Errorf("failed to create moduleconfig %s: %w", moduleName, err)
	}

	return nil
}

// UpdateModuleConfig updates an existing ModuleConfig resource
func UpdateModuleConfig(ctx context.Context, config *rest.Config, moduleName string, version int, enabled bool, settings map[string]interface{}) error {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	existing := &deckhousev1alpha1.ModuleConfig{}
	key := client.ObjectKey{Name: moduleName}
	if err := cl.client.Get(ctx, key, existing); err != nil {
		return fmt.Errorf("failed to get moduleconfig %s: %w", moduleName, err)
	}

	existing.Spec = deckhousev1alpha1.ModuleConfigSpec{
		Version:  version,
		Enabled:  &enabled,
		Settings: deckhousev1alpha1.SettingsValues(settings),
	}

	if err := cl.client.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to update moduleconfig %s: %w", moduleName, err)
	}

	return nil
}

// CreateModulePullOverride creates a new ModulePullOverride resource
func CreateModulePullOverride(ctx context.Context, config *rest.Config, moduleName string, imageTag string) error {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	// Parse imageTag as Duration for ScanInterval (default: 1m)
	scanInterval, err := time.ParseDuration("1m")
	if err != nil {
		return fmt.Errorf("failed to parse default scan interval: %w", err)
	}

	modulePullOverride := &deckhousev1alpha2.ModulePullOverride{
		ObjectMeta: metav1.ObjectMeta{
			Name: moduleName,
		},
		Spec: deckhousev1alpha2.ModulePullOverrideSpec{
			ImageTag:     imageTag,
			ScanInterval: libapi.Duration{Duration: scanInterval},
			Rollback:     false,
		},
	}

	if err := cl.client.Create(ctx, modulePullOverride); err != nil {
		return fmt.Errorf("failed to create modulepulloverride %s: %w", moduleName, err)
	}

	return nil
}

// UpdateModulePullOverride updates an existing ModulePullOverride resource
func UpdateModulePullOverride(ctx context.Context, config *rest.Config, moduleName string, imageTag string) error {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	existing := &deckhousev1alpha2.ModulePullOverride{}
	key := client.ObjectKey{Name: moduleName}
	if err := cl.client.Get(ctx, key, existing); err != nil {
		return fmt.Errorf("failed to get modulepulloverride %s: %w", moduleName, err)
	}

	// Parse imageTag as Duration for ScanInterval (default: 1m)
	scanInterval, err := time.ParseDuration("1m")
	if err != nil {
		return fmt.Errorf("failed to parse default scan interval: %w", err)
	}

	existing.Spec = deckhousev1alpha2.ModulePullOverrideSpec{
		ImageTag:     imageTag,
		ScanInterval: libapi.Duration{Duration: scanInterval},
		Rollback:     false,
	}

	if err := cl.client.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to update modulepulloverride %s: %w", moduleName, err)
	}

	return nil
}
