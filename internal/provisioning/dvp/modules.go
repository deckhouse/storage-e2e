/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dvp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/deckhouse"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

const devRegistryPrefix = "dev-"

const defaultModulePullTag = "main"

var moduleApplyRetry = retry.Config{
	MaxRetries:  30,
	InitialWait: 2 * time.Second,
	MaxWait:     30 * time.Second,
	Backoff:     1.5,
	LogRetries:  true,
}

type moduleApplier interface {
	apply(ctx context.Context, mc *config.ModuleConfig, registryRepo string) error
	waitReady(ctx context.Context, moduleName string, timeout time.Duration) error
}

func buildModuleLevels(modules []*config.ModuleConfig) ([][]*config.ModuleConfig, error) {
	byName := make(map[string]*config.ModuleConfig, len(modules))
	for _, m := range modules {
		if m == nil {
			return nil, fmt.Errorf("nil module config")
		}
		if _, dup := byName[m.Name]; dup {
			return nil, fmt.Errorf("duplicate module %q", m.Name)
		}
		byName[m.Name] = m
	}

	indegree := make(map[string]int, len(modules))
	dependents := make(map[string][]string) // dependency name -> modules that require it
	for _, m := range modules {
		indegree[m.Name] = 0
	}
	for _, m := range modules {
		seen := make(map[string]bool, len(m.Dependencies))
		for _, dep := range m.Dependencies {
			if dep == m.Name {
				return nil, fmt.Errorf("module %q depends on itself", m.Name)
			}
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("module %q depends on unknown module %q", m.Name, dep)
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			indegree[m.Name]++
			dependents[dep] = append(dependents[dep], m.Name)
		}
	}

	var levels [][]*config.ModuleConfig
	remaining := len(modules)
	for remaining > 0 {
		var names []string
		for name, deg := range indegree {
			if deg == 0 {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			var stuck []string
			for name := range indegree {
				stuck = append(stuck, name)
			}
			sort.Strings(stuck)
			return nil, fmt.Errorf("circular dependency among modules: %v", stuck)
		}
		sort.Strings(names)

		level := make([]*config.ModuleConfig, len(names))
		for i, name := range names {
			level[i] = byName[name]
			delete(indegree, name)
		}
		for _, name := range names {
			for _, dep := range dependents[name] {
				if _, ok := indegree[dep]; ok {
					indegree[dep]--
				}
			}
		}
		levels = append(levels, level)
		remaining -= len(names)
	}
	return levels, nil
}

func enableModulesInLevels(ctx context.Context, applier moduleApplier, modules []*config.ModuleConfig, registryRepo string, levelTimeout time.Duration) error {
	if len(modules) == 0 {
		return nil
	}

	levels, err := buildModuleLevels(modules)
	if err != nil {
		return fmt.Errorf("order modules: %w", err)
	}

	for levelIdx, level := range levels {
		applyGroup, applyCtx := errgroup.WithContext(ctx)
		for _, mc := range level {
			applyGroup.Go(func() error {
				if err := applier.apply(applyCtx, mc, registryRepo); err != nil {
					return fmt.Errorf("apply module %q (level %d): %w", mc.Name, levelIdx, err)
				}
				return nil
			})
		}
		if err := applyGroup.Wait(); err != nil {
			return err
		}

		readyGroup, readyCtx := errgroup.WithContext(ctx)
		for _, mc := range level {
			if !mc.Enabled {
				continue
			}
			readyGroup.Go(func() error {
				if err := applier.waitReady(readyCtx, mc.Name, levelTimeout); err != nil {
					return fmt.Errorf("module %q not ready (level %d): %w", mc.Name, levelIdx, err)
				}
				return nil
			})
		}
		if err := readyGroup.Wait(); err != nil {
			return err
		}
	}
	return nil
}

func (p *dvpProvider) enableModules(ctx context.Context, target *rest.Config, def *config.ClusterDefinition) error {
	applier := defaultModuleApplier{kube: target, logger: p.logger}
	return enableModulesInLevels(ctx, applier, def.DKPParameters.Modules, def.DKPParameters.RegistryRepo, config.ModuleDeployTimeout)
}

type defaultModuleApplier struct {
	kube   *rest.Config
	logger *slog.Logger
}

func (a defaultModuleApplier) apply(ctx context.Context, mc *config.ModuleConfig, registryRepo string) error {
	if err := upsertModuleConfig(ctx, a.kube, mc); err != nil {
		return err
	}
	return upsertModulePullOverride(ctx, a.kube, mc, registryRepo)
}

func (a defaultModuleApplier) waitReady(ctx context.Context, moduleName string, timeout time.Duration) error {
	return kubernetes.WaitForModuleReady(ctx, a.kube, moduleName, timeout)
}

func upsertModuleConfig(ctx context.Context, kube *rest.Config, mc *config.ModuleConfig) error {
	settings := mc.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	return retry.DoVoid(ctx, moduleApplyRetry, "configure ModuleConfig "+mc.Name, func() error {
		if _, err := deckhouse.GetModuleConfig(ctx, kube, mc.Name); err != nil {
			if apierrors.IsNotFound(err) {
				return deckhouse.CreateModuleConfig(ctx, kube, mc.Name, mc.Version, mc.Enabled, settings)
			}
			return err
		}
		return deckhouse.UpdateModuleConfig(ctx, kube, mc.Name, mc.Version, mc.Enabled, settings)
	})
}

func upsertModulePullOverride(ctx context.Context, kube *rest.Config, mc *config.ModuleConfig, registryRepo string) error {
	if !strings.HasPrefix(registryRepo, devRegistryPrefix) {
		return nil
	}
	imageTag := mc.ModulePullOverride
	if imageTag == "" {
		imageTag = defaultModulePullTag
	}
	return retry.DoVoid(ctx, moduleApplyRetry, "configure ModulePullOverride "+mc.Name, func() error {
		if _, err := deckhouse.GetModulePullOverride(ctx, kube, mc.Name); err != nil {
			if apierrors.IsNotFound(err) {
				return deckhouse.CreateModulePullOverride(ctx, kube, mc.Name, imageTag)
			}
			return err
		}
		return deckhouse.UpdateModulePullOverride(ctx, kube, mc.Name, imageTag)
	})
}
