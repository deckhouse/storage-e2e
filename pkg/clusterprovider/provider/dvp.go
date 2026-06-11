package provider

import (
	"context"

	"github.com/caarlos0/env/v11"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider/config"
)

func init() {
	DefaultRegistry.Register(ModeDvp, newDVPProvider)
}

// dvpProvider provisions clusters using the DVP (Deckhouse Virtualization
// Platform) strategy.
type dvpProvider struct {
	cfg     *config.ClusterConfig
	dvpConf *dvpConfig
}

type dvpConfig struct {
	ClusterBootstrapConfig string `env:"YAML_CONFIG_FILENAME,required"`
}

func (p *dvpProvider) Name() string { return ModeDvp }

func (p *dvpProvider) Bootstrap(ctx context.Context) error {
	panic("not implemented")
}

func (p *dvpProvider) Teardown(ctx context.Context) error {
	// TODO: implement — idempotent teardown by deterministic cluster name.
	panic("not implemented")
}

// newDVPProvider builds a dvpProvider, loading the DVP-specific env. Registered
// as the dvp strategy's Constructor.
func newDVPProvider(config *config.ClusterConfig) (Provider, error) {
	dvpConf := &dvpConfig{}
	if err := env.Parse(dvpConf); err != nil {
		return nil, err
	}

	return &dvpProvider{
		cfg:     config,
		dvpConf: dvpConf,
	}, nil
}
