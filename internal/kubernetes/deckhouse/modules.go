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

	deckhousev1alpha1 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha1"
	deckhousev1alpha2 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha2"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
