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

package config

import (
	"strings"
	"testing"
)

func lookupFrom(env map[string]string) EnvLookup {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func defWithOverrides(overrides ...string) *ClusterDefinition {
	modules := make([]*ModuleConfig, 0, len(overrides))
	for i, o := range overrides {
		modules = append(modules, &ModuleConfig{
			Name:               "module-" + string(rune('a'+i)),
			ModulePullOverride: o,
		})
	}
	return &ClusterDefinition{DKPParameters: DKPParameters{Modules: modules}}
}

func TestResolveModulePullOverrides(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		env     map[string]string
		want    string
		wantErr []string // substrings expected in the error; empty means success
	}{
		{
			name:  "literal tag untouched",
			input: "main",
			want:  "main",
		},
		{
			name:  "empty override untouched",
			input: "",
			want:  "",
		},
		{
			name:  "single reference resolved",
			input: "${MODULE_IMAGE_TAG}",
			env:   map[string]string{"MODULE_IMAGE_TAG": "pr131"},
			want:  "pr131",
		},
		{
			name:  "multiple references in one value",
			input: "${PREFIX}-${NAME}",
			env:   map[string]string{"PREFIX": "branch", "NAME": "ms-crc"},
			want:  "branch-ms-crc",
		},
		{
			name:    "unset reference reported",
			input:   "${MISSING_TAG}",
			wantErr: []string{"module-a", "MISSING_TAG", "unset"},
		},
		{
			name:    "malformed reference reported",
			input:   "${bad-name}",
			wantErr: []string{"module-a", "malformed", "bad-name"},
		},
		{
			name:  "bare dollar is a literal, not a reference",
			input: "tag-$NAME",
			env:   map[string]string{"NAME": "should-not-be-used"},
			want:  "tag-$NAME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := defWithOverrides(tt.input)
			err := ResolveModulePullOverrides(def, lookupFrom(tt.env))

			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err.Error(), want)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := def.DKPParameters.Modules[0].ModulePullOverride; got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveModulePullOverrides_PerModuleVariables(t *testing.T) {
	def := defWithOverrides("${CSI_CEPH_TAG}", "${SDS_ELASTIC_TAG}", "main")
	env := map[string]string{"CSI_CEPH_TAG": "pr131", "SDS_ELASTIC_TAG": "mr41"}

	if err := ResolveModulePullOverrides(def, lookupFrom(env)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"pr131", "mr41", "main"}
	for i, w := range want {
		if got := def.DKPParameters.Modules[i].ModulePullOverride; got != w {
			t.Errorf("module %d: got %q, want %q", i, got, w)
		}
	}
}

func TestResolveModulePullOverrides_AggregatesAllProblems(t *testing.T) {
	def := defWithOverrides("${MISSING_ONE}", "${MISSING_TWO}", "${good}")
	err := ResolveModulePullOverrides(def, lookupFrom(nil))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	for _, want := range []string{"MISSING_ONE", "MISSING_TWO", "good"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should report %q", err.Error(), want)
		}
	}
}

func TestResolveModulePullOverrides_NilSafety(t *testing.T) {
	if err := ResolveModulePullOverrides(nil, lookupFrom(nil)); err != nil {
		t.Fatalf("nil definition: unexpected error: %v", err)
	}

	def := &ClusterDefinition{DKPParameters: DKPParameters{
		Modules: []*ModuleConfig{nil, {Name: "csi-ceph", ModulePullOverride: "main"}},
	}}
	if err := ResolveModulePullOverrides(def, lookupFrom(nil)); err != nil {
		t.Fatalf("nil module entry: unexpected error: %v", err)
	}
}
