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

import "time"

// Configuration parameters used in code

// DefaultSetupVM is the default VM configuration of the node that is used for bootstrap of test cluster.
// This VM is always created separately and should be deleted after cluster bootstrap.
var DefaultSetupVM = ClusterNode{
	Hostname: "bootstrap-node-",
	HostType: HostTypeVM,
	Role:     ClusterRoleSetup,
	OSType:   OSTypeMap["Ubuntu 22.04 6.2.0-39-generic"],
	CPU:      2,
	RAM:      4,
	DiskSize: 20,
}

// VMsRunningTimeout is the timeout for waiting for all VMs to become Running state
const VMsRunningTimeout = 20 * time.Minute
