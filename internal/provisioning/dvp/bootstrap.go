/*
Copyright 2026 Flant JSC

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

package dvp

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"text/template"

	"github.com/deckhouse/storage-e2e/internal/config"
)

//go:embed bootstrap.tpl
var bootstrapTemplate string

// bootstrapTmpl is parsed once at package load; reused on every render.
var bootstrapTmpl = template.Must(template.New("bootstrap-config").Parse(bootstrapTemplate))

// bootstrapParams are the inputs to the dhctl bootstrap config template.
type bootstrapParams struct {
	PodSubnetCIDR        string
	ServiceSubnetCIDR    string
	KubernetesVersion    string
	ClusterDomain        string
	ImagesRepo           string
	RegistryDockerCfg    string
	PublicDomainTemplate string
	InternalNetworkCIDR  string
	DevBranch            string
}

// renderBootstrapConfig renders the embedded bootstrap template with p.
func renderBootstrapConfig(p bootstrapParams) ([]byte, error) {
	var buf bytes.Buffer
	if err := bootstrapTmpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("render bootstrap template: %w", err)
	}
	return buf.Bytes(), nil
}

// buildBootstrapParams derives template inputs from a cluster definition whose
// VM nodes already have IPAddress filled. registryDockerCfg is supplied by the
// caller (from dvp.Config), not read from any global.
func buildBootstrapParams(def *config.ClusterDefinition, registryDockerCfg string) (bootstrapParams, error) {
	if def == nil {
		return bootstrapParams{}, fmt.Errorf("cluster definition is nil")
	}

	// Validate required DKP fields here rather than assuming def.Validate() ran.
	required := []struct {
		value string
		msg   string
	}{
		{def.DKPParameters.PodSubnetCIDR, "dkpParameters.podSubnetCIDR is required"},
		{def.DKPParameters.ServiceSubnetCIDR, "dkpParameters.serviceSubnetCIDR is required"},
		{def.DKPParameters.KubernetesVersion, "dkpParameters.kubernetesVersion is required"},
		{def.DKPParameters.ClusterDomain, "dkpParameters.clusterDomain is required"},
		{def.DKPParameters.RegistryRepo, "dkpParameters.registryRepo is required"},
	}
	for _, r := range required {
		if r.value == "" {
			return bootstrapParams{}, errors.New(r.msg)
		}
	}

	var vmIPs []string
	firstMasterIP := ""
	for _, m := range def.Masters {
		if m.HostType == config.HostTypeVM && m.IPAddress != "" {
			vmIPs = append(vmIPs, m.IPAddress)
			if firstMasterIP == "" {
				firstMasterIP = m.IPAddress
			}
		}
	}
	for _, w := range def.Workers {
		if w.HostType == config.HostTypeVM && w.IPAddress != "" {
			vmIPs = append(vmIPs, w.IPAddress)
		}
	}
	if def.Setup != nil && def.Setup.HostType == config.HostTypeVM && def.Setup.IPAddress != "" {
		vmIPs = append(vmIPs, def.Setup.IPAddress)
	}

	if len(vmIPs) == 0 {
		return bootstrapParams{}, fmt.Errorf("no VM IP addresses in cluster definition (provision VMs first)")
	}
	if firstMasterIP == "" {
		return bootstrapParams{}, fmt.Errorf("no master IP address in cluster definition")
	}

	cidr, err := calculateNetworkCIDR(vmIPs)
	if err != nil {
		return bootstrapParams{}, fmt.Errorf("calculate internal network CIDR: %w", err)
	}

	devBranch := def.DKPParameters.DevBranch
	if devBranch == "" {
		devBranch = "main"
	}

	return bootstrapParams{
		PodSubnetCIDR:        def.DKPParameters.PodSubnetCIDR,
		ServiceSubnetCIDR:    def.DKPParameters.ServiceSubnetCIDR,
		KubernetesVersion:    def.DKPParameters.KubernetesVersion,
		ClusterDomain:        def.DKPParameters.ClusterDomain,
		ImagesRepo:           def.DKPParameters.RegistryRepo,
		RegistryDockerCfg:    registryDockerCfg,
		PublicDomainTemplate: fmt.Sprintf("%%s.%s.sslip.io", firstMasterIP),
		InternalNetworkCIDR:  cidr,
		DevBranch:            devBranch,
	}, nil
}

// calculateNetworkCIDR returns the smallest /24../16 network containing all IPs.
func calculateNetworkCIDR(vmIPs []string) (string, error) {
	if len(vmIPs) == 0 {
		return "", fmt.Errorf("vmIPs cannot be empty")
	}

	parsed := make([]net.IP, 0, len(vmIPs))
	for _, s := range vmIPs {
		ip := net.ParseIP(s)
		if ip == nil {
			return "", fmt.Errorf("invalid IP address: %s", s)
		}
		ip4 := ip.To4()
		if ip4 == nil {
			return "", fmt.Errorf("IP address is not IPv4: %s", s)
		}
		parsed = append(parsed, ip4)
	}

	base := make(net.IP, len(parsed[0]))
	copy(base, parsed[0])
	base[3] = 0

	for prefix := 24; prefix >= 16; prefix-- {
		mask := net.CIDRMask(prefix, 32)
		network := base.Mask(mask)
		_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", network.String(), prefix))
		if err != nil {
			return "", fmt.Errorf("parse CIDR: %w", err)
		}
		all := true
		for _, ip := range parsed {
			if !ipNet.Contains(ip) {
				all = false
				break
			}
		}
		if all {
			return ipNet.String(), nil
		}
	}
	return "", fmt.Errorf("no /16../24 network contains all IPs")
}
