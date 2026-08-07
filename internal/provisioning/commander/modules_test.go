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

package commander

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServesModuleConfigs(t *testing.T) {
	moduleConfigs := &metav1.APIResourceList{
		GroupVersion: moduleConfigGroupVersion,
		APIResources: []metav1.APIResource{
			{Name: "modulesources"},
			{Name: moduleConfigResource},
		},
	}
	core := &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "pods"}},
	}

	cases := []struct {
		name  string
		lists []*metav1.APIResourceList
		want  bool
	}{
		{"nothing discovered", nil, false},
		{"only core", []*metav1.APIResourceList{core}, false},
		{"moduleconfigs served", []*metav1.APIResourceList{core, moduleConfigs}, true},
		{
			// The window the bootstrap wait exists to sit out.
			"group present without the resource",
			[]*metav1.APIResourceList{{
				GroupVersion: moduleConfigGroupVersion,
				APIResources: []metav1.APIResource{{Name: "modulesources"}},
			}},
			false,
		},
		{
			"wrong version",
			[]*metav1.APIResourceList{{
				GroupVersion: "deckhouse.io/v1",
				APIResources: []metav1.APIResource{{Name: moduleConfigResource}},
			}},
			false,
		},
		{
			// ServerGroupsAndResources returns partial results on error.
			"nil entry is skipped",
			[]*metav1.APIResourceList{nil, moduleConfigs},
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := servesModuleConfigs(tc.lists); got != tc.want {
				t.Fatalf("servesModuleConfigs() = %v, want %v", got, tc.want)
			}
		})
	}
}
