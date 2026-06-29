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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	managedByLabelKey   = "storage-e2e.deckhouse.io/managed-by"
	managedByLabelValue = "storage-e2e"
)

// managedLabels are the labels stamped on every resource the provisioner
// creates. Isolation boundary is the namespace (one run per namespace), so no
// per-run label is needed.
func managedLabels() map[string]string {
	return map[string]string{
		managedByLabelKey: managedByLabelValue,
	}
}

// isManaged reports whether a resource was created by this provisioner and is
// therefore eligible for teardown.
func isManaged(meta metav1.ObjectMeta) bool {
	return meta.Labels[managedByLabelKey] == managedByLabelValue
}
