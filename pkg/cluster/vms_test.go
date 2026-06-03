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

package cluster

import (
	"strings"
	"testing"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func TestGetCVMINameFromImageURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"strips .img extension", "https://example.com/jammy-server.img", "jammy-server"},
		{"strips .qcow2 extension", "https://example.com/redos.qcow2", "redos"},
		{"lowercases", "https://EXAMPLE.com/UBUNTU-22.04.img", "ubuntu-22-04"},
		{"underscores to hyphens", "https://example.com/my_image_file.img", "my-image-file"},
		{"dots to hyphens", "https://example.com/v1.0.2-image.img", "v1-0-2-image"},
		{"collapses consecutive hyphens", "https://example.com/a__b..c.img", "a-b-c"},
		{"trims leading/trailing hyphens", "https://example.com/_..foo..img", "foo"},
		{
			"empty after sanitation -> 'image'",
			"https://example.com/._.qcow2",
			"image",
		},
		{
			"no path segments yet still works",
			"foo.img",
			"foo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := getCVMINameFromImageURL(tc.in)
			if got != tc.want {
				t.Errorf("getCVMINameFromImageURL(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestGetVMNodes(t *testing.T) {
	t.Run("filters VM nodes across masters/workers and includes VM setup", func(t *testing.T) {
		def := &config.ClusterDefinition{
			Masters: []config.ClusterNode{
				{Hostname: "m1", HostType: config.HostTypeVM},
				{Hostname: "m2", HostType: config.HostTypeBareMetal},
			},
			Workers: []config.ClusterNode{
				{Hostname: "w1", HostType: config.HostTypeBareMetal},
				{Hostname: "w2", HostType: config.HostTypeVM},
			},
			Setup: &config.ClusterNode{Hostname: "setup", HostType: config.HostTypeVM},
		}

		nodes := getVMNodes(def)
		got := hostnames(nodes)
		want := []string{"m1", "w2", "setup"}
		if !equalStrings(got, want) {
			t.Errorf("getVMNodes hostnames = %v, want %v", got, want)
		}
	})

	t.Run("bare-metal setup is excluded", func(t *testing.T) {
		def := &config.ClusterDefinition{
			Masters: []config.ClusterNode{{Hostname: "m1", HostType: config.HostTypeVM}},
			Setup:   &config.ClusterNode{Hostname: "setup-bm", HostType: config.HostTypeBareMetal},
		}
		nodes := getVMNodes(def)
		got := hostnames(nodes)
		if !equalStrings(got, []string{"m1"}) {
			t.Errorf("got %v, want [m1] (setup must be excluded when bare-metal)", got)
		}
	})

	t.Run("nil setup is fine", func(t *testing.T) {
		def := &config.ClusterDefinition{
			Workers: []config.ClusterNode{{Hostname: "w1", HostType: config.HostTypeVM}},
		}
		nodes := getVMNodes(def)
		if len(nodes) != 1 || nodes[0].Hostname != "w1" {
			t.Errorf("got %v, want [w1]", hostnames(nodes))
		}
	})
}

func TestGetSetupNode(t *testing.T) {
	t.Run("nil clusterDef errors", func(t *testing.T) {
		_, err := GetSetupNode(nil)
		if err == nil {
			t.Fatal("expected error for nil clusterDef")
		}
	})

	t.Run("nil Setup errors", func(t *testing.T) {
		_, err := GetSetupNode(&config.ClusterDefinition{})
		if err == nil {
			t.Fatal("expected error when Setup is nil")
		}
	})

	t.Run("returns setup node", func(t *testing.T) {
		want := &config.ClusterNode{Hostname: "boot", HostType: config.HostTypeVM}
		def := &config.ClusterDefinition{Setup: want}
		got, err := GetSetupNode(def)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Errorf("got %p, want %p", got, want)
		}
	})
}

func TestGetNodeIPAddress(t *testing.T) {
	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", IPAddress: "10.0.0.1", HostType: config.HostTypeVM},
			{Hostname: "m-empty", IPAddress: "", HostType: config.HostTypeVM},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", IPAddress: "10.0.0.2", HostType: config.HostTypeVM},
		},
		Setup: &config.ClusterNode{Hostname: "boot", IPAddress: "10.0.0.99", HostType: config.HostTypeVM},
	}

	cases := []struct {
		name     string
		hostname string
		wantIP   string
		wantErr  string // substring; empty means no error expected
	}{
		{"master found", "m1", "10.0.0.1", ""},
		{"worker found", "w1", "10.0.0.2", ""},
		{"setup found", "boot", "10.0.0.99", ""},
		{"master IP empty -> error", "m-empty", "", "IP address not set for master"},
		{"unknown hostname -> error", "ghost", "", "not found in cluster definition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip, err := GetNodeIPAddress(def, tc.hostname)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ip != tc.wantIP {
					t.Errorf("ip=%q, want %q", ip, tc.wantIP)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestGenerateRandomSuffix(t *testing.T) {
	t.Run("length matches argument", func(t *testing.T) {
		for _, n := range []int{0, 1, 5, 32} {
			got := GenerateRandomSuffix(n)
			if len(got) != n {
				t.Errorf("len(GenerateRandomSuffix(%d))=%d, want %d (got=%q)", n, len(got), n, got)
			}
		}
	})

	t.Run("uses only lowercase alphanumerics", func(t *testing.T) {
		got := GenerateRandomSuffix(64)
		for _, r := range got {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
				t.Fatalf("invalid character %q in suffix %q", r, got)
			}
		}
	})

	t.Run("two calls usually differ for non-trivial length", func(t *testing.T) {
		// With charset of 36 and length 16 the collision probability is
		// 36^-16 — negligible. If it ever collides we have a bigger problem.
		a := GenerateRandomSuffix(16)
		b := GenerateRandomSuffix(16)
		if a == b {
			t.Errorf("two 16-char suffixes were identical: %q (probability ~36^-16)", a)
		}
	})
}

func TestGenerateCloudInitUserData(t *testing.T) {
	t.Run("worker cloud-init includes hostname and key", func(t *testing.T) {
		got := generateCloudInitUserData("worker-1", "ssh-ed25519 AAAA test@key")
		mustContain(t, got, "#cloud-config")
		mustContain(t, got, "hostnamectl set-hostname worker-1")
		mustContain(t, got, "ssh-ed25519 AAAA test@key")
		mustContain(t, got, "mirror.yandex.ru/ubuntu") // apt mirror tweak
		mustContain(t, got, "99force-ipv4")            // IPv4 apt override
	})

	t.Run("setup cloud-init includes docker and key", func(t *testing.T) {
		got := generateSetupNodeCloudInit("bootstrap-node-abc", "ssh-rsa BBBB me@host")
		mustContain(t, got, "#cloud-config")
		mustContain(t, got, "hostnamectl set-hostname bootstrap-node-abc")
		mustContain(t, got, "ssh-rsa BBBB me@host")
		mustContain(t, got, "docker.io")
		mustContain(t, got, "systemctl enable --now docker.service")
	})
}

// helpers ---------------------------------------------------------------

func hostnames(nodes []config.ClusterNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Hostname)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("missing %q in output:\n%s", substr, s)
	}
}
