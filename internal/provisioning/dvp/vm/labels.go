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

// Teardown only touches resources carrying this label, so anything the
// framework creates on the base cluster must carry it to be swept by Remove.
const (
	ManagedByLabelKey   = "storage-e2e.deckhouse.io/managed-by"
	ManagedByLabelValue = "storage-e2e"
)

func ManagedLabels() map[string]string {
	return map[string]string{
		ManagedByLabelKey: ManagedByLabelValue,
	}
}

func isManaged(meta metav1.ObjectMeta) bool {
	return meta.Labels[ManagedByLabelKey] == ManagedByLabelValue
}
