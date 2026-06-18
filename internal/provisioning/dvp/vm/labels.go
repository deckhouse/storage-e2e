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

// Package vm provisions and tears down the virtual machines that back a test
// cluster on a Deckhouse Virtualization (DVP) base cluster. It builds the
// ClusterVirtualImage -> VirtualDisk -> VirtualMachine (+ VirtualMachineClass)
// resource graph, waits for readiness, and gathers VM IP addresses.
//
// The package is split into pure spec builders (build.go, cloudinit.go),
// idempotent ensure operations (ensure.go), a generic wait helper (wait.go) and
// orchestrators (provision.go). All side-effecting inputs (setup-VM name suffix,
// SSH public key, storage class, poll interval) are injected through Config so
// the core stays deterministic and table-testable.
package vm

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// managedByLabelKey marks every resource created by this package so that a
	// teardown can find and delete them by label selector instead of relying on
	// an in-memory list of names.
	managedByLabelKey = "storage-e2e.deckhouse.io/managed-by"
	// managedByLabelValue is the constant value stored under managedByLabelKey.
	managedByLabelValue = "storage-e2e"
	// runLabelKey scopes resources to a single provisioning run. The value is
	// supplied by the caller (the DVP provider uses the target namespace) so
	// that a later, separate Remove invocation can rediscover the resources.
	runLabelKey = "storage-e2e.deckhouse.io/run"
)

// managedLabels returns the label set applied to every resource created for a
// given run.
func managedLabels(run string) map[string]string {
	return map[string]string{
		managedByLabelKey: managedByLabelValue,
		runLabelKey:       run,
	}
}

// isManagedByRun reports whether the object metadata carries this package's
// managed-by label and matches the given run scope.
func isManagedByRun(meta metav1.ObjectMeta, run string) bool {
	return meta.Labels[managedByLabelKey] == managedByLabelValue &&
		meta.Labels[runLabelKey] == run
}
