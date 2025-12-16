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
	"time"

	"gopkg.in/yaml.v3"
)

// HostType represents the type of host (VM or bare-metal)
type HostType string

const (
	HostTypeVM        HostType = "vm"
	HostTypeBareMetal HostType = "bare-metal"
)

// ClusterRole represents the role of a node in the cluster
type ClusterRole string

const (
	ClusterRoleMaster ClusterRole = "master"
	ClusterRoleWorker ClusterRole = "worker"
	ClusterRoleSetup  ClusterRole = "setup" // Bootstrap node for DKP installation
)

// OSType represents the operating system type
type OSType struct {
	Name          string
	ImageURL      string
	KernelVersion string
}

// ClusterNode defines a single node in the cluster
type ClusterNode struct {
	Hostname  string      `yaml:"hostname"`
	IPAddress string      `yaml:"ipAddress,omitempty"` // Required for bare-metal, optional for VM
	OSType    OSType      `yaml:"osType"`              // Required for VM, optional for bare-metal (custom unmarshaler handles string -> OSType conversion)
	HostType  HostType    `yaml:"hostType"`
	Role      ClusterRole `yaml:"role"`
	// VM-specific fields (only used when HostType == HostTypeVM)
	CPU      int `yaml:"cpu"`      // Required for VM
	RAM      int `yaml:"ram"`      // Required for VM, in GB
	DiskSize int `yaml:"diskSize"` // Required for VM, in GB
	// Bare-metal specific fields
	Prepared bool `yaml:"prepared,omitempty"` // Whether the node is already prepared for DKP installation
}

// DKPParameters defines DKP-specific parameters for cluster deployment
type DKPParameters struct {
	KubernetesVersion string          `yaml:"kubernetesVersion"`
	PodSubnetCIDR     string          `yaml:"podSubnetCIDR"`
	ServiceSubnetCIDR string          `yaml:"serviceSubnetCIDR"`
	ClusterDomain     string          `yaml:"clusterDomain"`
	RegistryRepo      string          `yaml:"registryRepo"`
	Modules           []*ModuleConfig `yaml:"modules,omitempty"`
}

// ClusterDefinition defines the complete cluster configuration
type ClusterDefinition struct {
	Masters       []ClusterNode `yaml:"masters"`
	Workers       []ClusterNode `yaml:"workers"`
	Setup         *ClusterNode  `yaml:"setup,omitempty"` // Bootstrap node (can be nil)
	DKPParameters DKPParameters `yaml:"dkpParameters"`
}

// ModuleConfig defines a Deckhouse module configuration
type ModuleConfig struct {
	Name               string         `yaml:"name"`
	Version            int            `yaml:"version"`
	Enabled            bool           `yaml:"enabled"`
	Settings           map[string]any `yaml:"settings,omitempty"`
	Dependencies       []string       `yaml:"dependencies,omitempty"`       // Names of modules that must be enabled before this one
	ModulePullOverride string         `yaml:"modulePullOverride,omitempty"` // Override the module pull branch or tag (e.g. "main", "pr123", "mr41"). Main is defailt value.
}

const (
	HostReadyTimeout    = 10 * time.Minute // Timeout for hosts to be ready
	DKPDeployTimeout    = 30 * time.Minute // Timeout for DKP deployment
	ModuleDeployTimeout = 10 * time.Minute // Timeout for module deployment
)

// UnmarshalYAML implements custom YAML unmarshaling for ClusterNode
// to handle OSType conversion from string key to OSType struct
func (n *ClusterNode) UnmarshalYAML(value *yaml.Node) error {
	// Temporary struct with OSType as string for unmarshaling
	type clusterNodeTmp struct {
		Hostname  string `yaml:"hostname"`
		IPAddress string `yaml:"ipAddress,omitempty"`
		OSType    string `yaml:"osType"`
		HostType  string `yaml:"hostType"`
		Role      string `yaml:"role"`
		CPU       int    `yaml:"cpu"`
		RAM       int    `yaml:"ram"`
		DiskSize  int    `yaml:"diskSize"`
		Prepared  bool   `yaml:"prepared,omitempty"`
	}

	var tmp clusterNodeTmp
	if err := value.Decode(&tmp); err != nil {
		return err
	}

	// Convert HostType
	hostType := HostType(tmp.HostType)
	if hostType != HostTypeVM && hostType != HostTypeBareMetal {
		return fmt.Errorf("invalid hostType: %s", tmp.HostType)
	}

	// Convert Role
	role := ClusterRole(tmp.Role)
	if role != ClusterRoleMaster && role != ClusterRoleWorker && role != ClusterRoleSetup {
		return fmt.Errorf("invalid role: %s", tmp.Role)
	}

	// Convert OSType string key to OSType struct
	osType, ok := OSTypeMap[tmp.OSType]
	if !ok {
		return fmt.Errorf("unknown osType: %s", tmp.OSType)
	}

	// Assign to actual struct
	n.Hostname = tmp.Hostname
	n.IPAddress = tmp.IPAddress
	n.OSType = osType
	n.HostType = hostType
	n.Role = role
	n.CPU = tmp.CPU
	n.RAM = tmp.RAM
	n.DiskSize = tmp.DiskSize
	n.Prepared = tmp.Prepared

	return nil
}

// UnmarshalYAML implements custom YAML unmarshaling for ClusterDefinition
// to handle the top-level "clusterDefinition:" key in the YAML
func (c *ClusterDefinition) UnmarshalYAML(value *yaml.Node) error {
	// Check if we have a top-level "clusterDefinition" key
	if value.Kind == yaml.MappingNode && len(value.Content) > 0 {
		// Look for "clusterDefinition" key
		for i := 0; i < len(value.Content)-1; i += 2 {
			if value.Content[i].Value == "clusterDefinition" {
				// Found the key, decode the value (next node) into a temporary struct
				// to avoid infinite recursion
				type clusterDefTmp struct {
					Masters       []ClusterNode `yaml:"masters"`
					Workers       []ClusterNode `yaml:"workers"`
					Setup         *ClusterNode  `yaml:"setup,omitempty"`
					DKPParameters DKPParameters `yaml:"dkpParameters"`
				}
				var tmp clusterDefTmp
				if err := value.Content[i+1].Decode(&tmp); err != nil {
					return err
				}
				// Copy to actual struct
				c.Masters = tmp.Masters
				c.Workers = tmp.Workers
				c.Setup = tmp.Setup
				c.DKPParameters = tmp.DKPParameters
				return nil
			}
		}
	}
	// If no "clusterDefinition" key found, decode directly using temporary struct
	// to avoid infinite recursion
	type clusterDefTmp struct {
		Masters       []ClusterNode `yaml:"masters"`
		Workers       []ClusterNode `yaml:"workers"`
		Setup         *ClusterNode  `yaml:"setup,omitempty"`
		DKPParameters DKPParameters `yaml:"dkpParameters"`
	}
	var tmp clusterDefTmp
	if err := value.Decode(&tmp); err != nil {
		return err
	}
	c.Masters = tmp.Masters
	c.Workers = tmp.Workers
	c.Setup = tmp.Setup
	c.DKPParameters = tmp.DKPParameters
	return nil
}
