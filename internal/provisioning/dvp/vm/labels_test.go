/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package vm

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagedLabelsOnlyManagedBy(t *testing.T) {
	labels := managedLabels()
	if got := labels[managedByLabelKey]; got != managedByLabelValue {
		t.Errorf("managed-by = %q, want %q", got, managedByLabelValue)
	}
	if len(labels) != 1 {
		t.Errorf("labels = %v, want exactly one (managed-by)", labels)
	}
}

func TestIsManaged(t *testing.T) {
	managed := metav1.ObjectMeta{Labels: managedLabels()}
	if !isManaged(managed) {
		t.Error("isManaged(managed) = false, want true")
	}

	unmanaged := metav1.ObjectMeta{Labels: map[string]string{"foo": "bar"}}
	if isManaged(unmanaged) {
		t.Error("isManaged(unmanaged) = true, want false")
	}

	none := metav1.ObjectMeta{}
	if isManaged(none) {
		t.Error("isManaged(no labels) = true, want false")
	}
}
