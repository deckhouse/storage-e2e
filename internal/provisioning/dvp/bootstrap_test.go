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
	"strings"
	"testing"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func TestRenderBootstrapConfig(t *testing.T) {
	t.Parallel()

	p := bootstrapParams{
		PodSubnetCIDR:        "10.112.0.0/16",
		ServiceSubnetCIDR:    "10.225.0.0/16",
		KubernetesVersion:    "Automatic",
		ClusterDomain:        "cluster.local",
		ImagesRepo:           "dev-registry.deckhouse.io/sys/deckhouse-oss",
		RegistryDockerCfg:    "eyJhdXRocyI6e30=",
		PublicDomainTemplate: "%s.10.10.1.5.sslip.io",
		InternalNetworkCIDR:  "10.10.1.0/24",
		DevBranch:            "main",
	}

	out, err := renderBootstrapConfig(p)
	if err != nil {
		t.Fatalf("renderBootstrapConfig() error = %v", err)
	}
	s := string(out)

	for _, want := range []string{
		"podSubnetCIDR: 10.112.0.0/16",
		"serviceSubnetCIDR: 10.225.0.0/16",
		`kubernetesVersion: "Automatic"`,
		`clusterDomain: "cluster.local"`,
		"imagesRepo: dev-registry.deckhouse.io/sys/deckhouse-oss",
		"registryDockerCfg: eyJhdXRocyI6e30=",
		"devBranch: main",
		`publicDomainTemplate: "%s.10.10.1.5.sslip.io"`,
		"- 10.10.1.0/24",
		"kind: ClusterConfiguration",
		"kind: StaticClusterConfiguration",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered config missing %q\n---\n%s", want, s)
		}
	}
}

func TestCalculateNetworkCIDR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ips  []string
		want string
		err  bool
	}{
		{"single /24", []string{"10.10.1.5"}, "10.10.1.0/24", false},
		{"same /24", []string{"10.10.1.5", "10.10.1.9"}, "10.10.1.0/24", false},
		{"spans /23", []string{"10.10.0.5", "10.10.1.9"}, "10.10.0.0/23", false},
		{"invalid ip", []string{"not-an-ip"}, "", true},
		{"ipv6 rejected", []string{"::1"}, "", true},
		{"empty", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateNetworkCIDR(tt.ips)
			if tt.err {
				if err == nil {
					t.Fatalf("calculateNetworkCIDR(%v) err = nil, want error", tt.ips)
				}
				return
			}
			if err != nil {
				t.Fatalf("calculateNetworkCIDR(%v) err = %v", tt.ips, err)
			}
			if got != tt.want {
				t.Errorf("calculateNetworkCIDR(%v) = %q, want %q", tt.ips, got, tt.want)
			}
		})
	}
}

func TestBuildBootstrapParams(t *testing.T) {
	t.Parallel()

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.5"},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", HostType: config.HostTypeVM, IPAddress: "10.10.1.6"},
		},
	}
	def.DKPParameters.PodSubnetCIDR = "10.112.0.0/16"
	def.DKPParameters.ServiceSubnetCIDR = "10.225.0.0/16"
	def.DKPParameters.KubernetesVersion = "Automatic"
	def.DKPParameters.ClusterDomain = "cluster.local"
	def.DKPParameters.RegistryRepo = "dev-registry.deckhouse.io/sys/deckhouse-oss"
	// DevBranch intentionally empty → defaults to main.

	p, err := buildBootstrapParams(def, "DOCKERCFG")
	if err != nil {
		t.Fatalf("buildBootstrapParams() error = %v", err)
	}
	if p.InternalNetworkCIDR != "10.10.1.0/24" {
		t.Errorf("InternalNetworkCIDR = %q, want 10.10.1.0/24", p.InternalNetworkCIDR)
	}
	if p.PublicDomainTemplate != "%s.10.10.1.5.sslip.io" {
		t.Errorf("PublicDomainTemplate = %q", p.PublicDomainTemplate)
	}
	if p.DevBranch != "main" {
		t.Errorf("DevBranch = %q, want main (default)", p.DevBranch)
	}
	if p.RegistryDockerCfg != "DOCKERCFG" {
		t.Errorf("RegistryDockerCfg = %q, want DOCKERCFG", p.RegistryDockerCfg)
	}
	if p.ImagesRepo != "dev-registry.deckhouse.io/sys/deckhouse-oss" {
		t.Errorf("ImagesRepo = %q", p.ImagesRepo)
	}
}

func TestBuildBootstrapParamsNoIPs(t *testing.T) {
	t.Parallel()
	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{{Hostname: "m1", HostType: config.HostTypeVM}},
	}
	if _, err := buildBootstrapParams(def, "x"); err == nil {
		t.Error("buildBootstrapParams() err = nil, want error when no IPs filled")
	}
}
