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

package kubernetes

import (
	"reflect"
	"sort"
	"testing"
)

func TestFindUnsetEnvVars(t *testing.T) {
	t.Run("returns deduped names for unset vars only", func(t *testing.T) {
		const (
			unsetA = "STORAGE_E2E_TEST_UNSET_A"
			unsetB = "STORAGE_E2E_TEST_UNSET_B"
			setOK  = "STORAGE_E2E_TEST_SET_OK"
		)
		// Explicitly empty the probe vars so the test is hermetic even if
		// something in the environment happened to set them.
		t.Setenv(unsetA, "")
		t.Setenv(unsetB, "")
		t.Setenv(setOK, "value-is-here")

		content := `image: ${` + unsetA + `}
extra: ${` + unsetA + `}
ref: ${` + unsetB + `}
tag: ${` + setOK + `}`

		got := FindUnsetEnvVars(content)
		sort.Strings(got)
		want := []string{unsetA, unsetB}
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("FindUnsetEnvVars()=%v, want %v", got, want)
		}
	})

	t.Run("ignores non-matching tokens", func(t *testing.T) {
		// `$VAR` (no braces) and `${1foo}` (starts with digit) must be ignored;
		// only the well-formed `${valid_NAME}` should be reported. Force the var
		// empty (empty -> treated as unset); highly unlikely to collide.
		t.Setenv("valid_NAME", "")
		got := FindUnsetEnvVars(`a: $PLAIN
b: ${1invalid}
c: ${valid_NAME}`)
		if !reflect.DeepEqual(got, []string{"valid_NAME"}) {
			t.Fatalf("got %v, want [valid_NAME]", got)
		}
	})

	t.Run("empty content returns nil", func(t *testing.T) {
		if got := FindUnsetEnvVars(""); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}

func TestSplitYAMLDocuments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single document",
			in:   "kind: A\nmetadata:\n  name: a\n",
			want: []string{"kind: A\nmetadata:\n  name: a"},
		},
		{
			name: "two documents joined by \\n---\\n",
			in:   "kind: A\n---\nkind: B\n",
			want: []string{"kind: A", "kind: B"},
		},
		{
			name: "trailing separator yields no extra empty doc",
			in:   "kind: A\n---\n",
			want: []string{"kind: A"},
		},
		{
			name: "leading separator skipped",
			in:   "---\nkind: A\n",
			// "---\nkind: A\n" has no "\n---\n" separator (the leading "---" is at line 0).
			// Strings.Split returns the whole thing as one entry; "---\nkind: A" remains
			// after TrimSpace and is kept because it isn't bare "---".
			want: []string{"---\nkind: A"},
		},
		{
			name: "only separators -> nothing",
			in:   "\n---\n\n---\n",
			want: nil,
		},
		{
			name: "empty input returns nil",
			in:   "",
			want: nil,
		},
		{
			name: "three docs with whitespace",
			in:   "  kind: A  \n---\nkind: B\n---\nkind: C",
			want: []string{"kind: A", "kind: B", "kind: C"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitYAMLDocuments(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitYAMLDocuments(%q)=\n  %#v\nwant\n  %#v", tc.in, got, tc.want)
			}
		})
	}
}
