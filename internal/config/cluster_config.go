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

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadClusterDefinition(path string) (*ClusterDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cluster config %q: %w", path, err)
	}

	var def ClusterDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse cluster config %q: %w", path, err)
	}

	if err := ResolveModulePullOverrides(&def, os.LookupEnv); err != nil {
		return nil, fmt.Errorf("resolve cluster config %q: %w", path, err)
	}

	if err := def.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster config %q: %w", path, err)
	}

	return &def, nil
}

func (c *ClusterDefinition) Validate() error {
	if len(c.Masters) == 0 {
		return fmt.Errorf("at least one master node is required")
	}

	dkp := c.DKPParameters
	if dkp.PodSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.podSubnetCIDR is required")
	}
	if dkp.ServiceSubnetCIDR == "" {
		return fmt.Errorf("dkpParameters.serviceSubnetCIDR is required")
	}
	if dkp.ClusterDomain == "" {
		return fmt.Errorf("dkpParameters.clusterDomain is required")
	}
	if dkp.RegistryRepo == "" {
		return fmt.Errorf("dkpParameters.registryRepo is required")
	}

	return nil
}
