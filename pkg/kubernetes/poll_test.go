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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFormatRef(t *testing.T) {
	cases := []struct {
		namespace, name, want string
	}{
		{"", "cluster-scoped", "cluster-scoped"},
		{"ns", "foo", "ns/foo"},
		{"", "", ""},
		{"ns", "", "ns/"},
	}
	for _, tc := range cases {
		got := formatRef(tc.namespace, tc.name)
		if got != tc.want {
			t.Errorf("formatRef(%q,%q)=%q, want %q", tc.namespace, tc.name, got, tc.want)
		}
	}
}

func TestSameFinalizers(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []string{}, []string{}, true},
		{"identical order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, false},
		{"different lengths", []string{"a"}, []string{"a", "b"}, false},
		{"duplicates differ", []string{"a", "a"}, []string{"a", "b"}, false},
		{"single equal", []string{"x"}, []string{"x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameFinalizers(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("sameFinalizers(%v,%v)=%v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestErrIfTerminating(t *testing.T) {
	t.Run("no deletionTimestamp returns nil", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("foo")
		if err := errIfTerminating(obj, "Pod", "default/foo"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("deletionTimestamp set returns descriptive error", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("foo")
		obj.SetNamespace("default")
		now := metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		obj.SetDeletionTimestamp(&now)
		obj.SetFinalizers([]string{"kubernetes.io/pvc-protection"})

		err := errIfTerminating(obj, "Pod", "default/foo")
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		for _, want := range []string{
			"Pod",
			"default/foo",
			"deletionTimestamp",
			"kubernetes.io/pvc-protection",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("error missing %q: %v", want, msg)
			}
		}
	})
}
