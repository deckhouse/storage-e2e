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

	"github.com/caarlos0/env/v11"
	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

func init() {
	clusterprovider.DefaultRegistry.Register(clusterprovider.ModeDVP, NewDVPProvider)
}

// dvpProvider provisions clusters using the DVP (Deckhouse Virtualization
// Platform) strategy.
type dvpProvider struct {
	cfg     *clusterprovider.ClusterConfig
	dvpConf *Config
	logger  *slog.Logger
}

// NewDVPProvider builds a dvpProvider, loading the DVP-specific env. Registered
// as the dvp strategy's Constructor.
func NewDVPProvider(logger *slog.Logger, config *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	dvpConf := &Config{}
	if err := env.Parse(dvpConf); err != nil {
		return nil, err
	}

	return &dvpProvider{
		cfg:     config,
		dvpConf: dvpConf,
		logger:  logger,
	}, nil
}

func (p *dvpProvider) Name() string { return clusterprovider.ModeDVP }

func (p *dvpProvider) Bootstrap(ctx context.Context) error {
	clusterDef, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return fmt.Errorf("load cluster bootstrap config: %w", err)
	}

	p.logger.Info("loaded cluster bootstrap config",
		"path", p.cfg.ClusterBootstrapConfigPath,
		"masters", len(clusterDef.Masters),
		"workers", len(clusterDef.Workers),
	)

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	// TODO: implement — idempotent teardown by deterministic cluster name.
	panic("not implemented")
}
