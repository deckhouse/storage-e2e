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
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
)

// PollGetTimeout caps a single Get call inside readiness pollers. Without
// this cap a hung TCP connect (e.g. SSH tunnel that died after a Wi-Fi flap
// on the developer's laptop) eats the entire parent timeout silently — the
// poller appears to "hang" until the per-resource ReadyTimeout fires 15-20
// minutes later. With a 30s cap each Get fails fast, so we surface the
// network problem early via the WARN log emitted by pollResourceUntilReady.
const PollGetTimeout = 30 * time.Second

// PollTickInterval is the default tick interval between Get attempts when
// waiting for a Kubernetes resource to reach a ready state.
const PollTickInterval = 5 * time.Second

// pollResourceUntilReady polls a single namespaced unstructured resource
// until isReady returns (true, "<reason>") or the parent timeout expires.
//
// It centralizes three behaviors that all of our Wait*Ready helpers want:
//   - per-call deadline (PollGetTimeout) on every Get, so a dead network
//     surfaces in seconds instead of after the readiness timeout;
//   - WARN logs with a counter when consecutive network errors happen — silent
//     pollers were the root cause of "test hangs forever after Wi-Fi flap";
//   - tolerance of NotFound (the resource may not have been seen by the
//     watch cache yet) and of `isReady=false` (still progressing).
//
// Parameters:
//
//   - kubeconfig:       rest config used to construct the dynamic client.
//   - gvr:              GroupVersionResource of the resource being polled.
//   - namespace, name:  scope of the resource. Must both be non-empty.
//   - readyTimeout:     overall budget. Returns timeout error after this.
//   - tickInterval:     gap between Get attempts. Pass PollTickInterval if
//     unsure; resources with slow reconcilers can use longer intervals.
//   - resourceLabel:    string used in log lines (e.g. "CephCluster"). Keep
//     short — the namespace/name is appended for context.
//   - isReady:          decider over the unstructured object. Returns
//     (ready, humanReason). If ready is true, pollResourceUntilReady
//     prints a Success log including the reason and returns nil.
func pollResourceUntilReady(
	ctx context.Context,
	kubeconfig *rest.Config,
	gvr schema.GroupVersionResource,
	namespace, name string,
	readyTimeout time.Duration,
	tickInterval time.Duration,
	resourceLabel string,
	isReady func(obj *unstructured.Unstructured) (ready bool, reason string),
) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if isReady == nil {
		return fmt.Errorf("isReady is required")
	}
	if tickInterval <= 0 {
		tickInterval = PollTickInterval
	}

	ref := formatRef(namespace, name)
	logger.Debug("Waiting for %s %s to become Ready (timeout: %v)", resourceLabel, ref, readyTimeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var consecutiveErrs int
	for {
		obj, err := getWithTimeout(deadlineCtx, dynamicClient, gvr, namespace, name, PollGetTimeout)
		switch {
		case err == nil:
			consecutiveErrs = 0
			// Refuse to wait for Ready on a Terminating object. Without this
			// short-circuit a stale `Deleting` CR (e.g. CephCluster left over
			// by a previous run that didn't finish teardown) would keep us
			// polling for the full readyTimeout: phase=Deleting never matches
			// any "Ready" condition. Failing fast here gives the operator a
			// chance to clean up (or strip finalizers) instead of hiding the
			// real state of the cluster behind a 15-20 minute timeout.
			if dt := obj.GetDeletionTimestamp(); dt != nil {
				return fmt.Errorf(
					"%s %s is being deleted (deletionTimestamp=%s, finalizers=%v); "+
						"refusing to wait for Ready on a Terminating object",
					resourceLabel, ref,
					dt.Format(time.RFC3339), obj.GetFinalizers(),
				)
			}
			if ready, reason := isReady(obj); ready {
				if reason != "" {
					logger.Success("%s %s is Ready (%s)", resourceLabel, ref, reason)
				} else {
					logger.Success("%s %s is Ready", resourceLabel, ref)
				}
				return nil
			}
		case apierrors.IsNotFound(err):
			// Resource hasn't propagated yet. Treat as "still progressing"
			// without warning so we don't spam logs on healthy clusters that
			// just haven't observed the create yet.
			consecutiveErrs = 0
			logger.Debug("%s %s not found yet", resourceLabel, ref)
		default:
			consecutiveErrs++
			// Quiet the first two failures (spurious 5xx, leader re-election),
			// loud after that. Loud == WARN at every iteration so the user
			// can see the cluster connection is dying instead of waiting for
			// the readyTimeout to fire.
			if consecutiveErrs >= 3 {
				logger.Warn(
					"%s %s GET failed for %d consecutive iterations: %v",
					resourceLabel, ref, consecutiveErrs, err,
				)
			} else {
				logger.Debug("Error getting %s %s: %v", resourceLabel, ref, err)
			}
		}

		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timeout waiting for %s %s: %w", resourceLabel, ref, deadlineCtx.Err())
		case <-ticker.C:
		}
	}
}

// PollGoneProgressEvery controls how often pollResourceUntilGone emits a
// progress INFO line while the resource is still alive. We don't want a log
// per tick (chatty) but we also don't want long stretches of silence when a
// finalizer is stuck for minutes — every ~30s strikes a balance.
const PollGoneProgressEvery = 30 * time.Second

// pollResourceUntilGone polls a single namespaced unstructured resource
// until a GET returns NotFound (i.e. the API server has GC'd the object) or
// the parent timeout expires.
//
// Mirrors pollResourceUntilReady but with inverted success criterion. Three
// behaviors worth calling out:
//   - per-call deadline (PollGetTimeout) on every Get;
//   - WARN logs after a few consecutive non-NotFound errors so a dropped
//     SSH tunnel surfaces in seconds rather than at the timeout;
//   - periodic INFO progress log including the object's deletionTimestamp
//     and finalizers — that's exactly the diagnostic info you need to know
//     why Rook hasn't finished tearing the resource down. We avoid logging
//     this on every tick (chatty) and instead emit at most once per
//     PollGoneProgressEvery.
func pollResourceUntilGone(
	ctx context.Context,
	kubeconfig *rest.Config,
	gvr schema.GroupVersionResource,
	namespace, name string,
	goneTimeout time.Duration,
	tickInterval time.Duration,
	resourceLabel string,
) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if tickInterval <= 0 {
		tickInterval = PollTickInterval
	}

	ref := formatRef(namespace, name)
	logger.Debug("Waiting for %s %s to be gone (timeout: %v)", resourceLabel, ref, goneTimeout)

	dynamicClient, err := NewDynamicClientWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, goneTimeout)
	defer cancel()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var (
		consecutiveErrs int
		lastProgress    time.Time
		lastFinalizers  []string
		lastDeletionTS  string
	)
	for {
		obj, err := getWithTimeout(deadlineCtx, dynamicClient, gvr, namespace, name, PollGetTimeout)
		switch {
		case apierrors.IsNotFound(err):
			logger.Success("%s %s is gone", resourceLabel, ref)
			return nil
		case err == nil:
			consecutiveErrs = 0
			finalizers := obj.GetFinalizers()
			deletionTS := ""
			if dt := obj.GetDeletionTimestamp(); dt != nil {
				deletionTS = dt.Format(time.RFC3339)
			}
			// Surface progress periodically OR whenever the visible state
			// changes (finalizers list shrunk, deletionTimestamp finally
			// appeared after a Delete request was missed, ...).
			stateChanged := deletionTS != lastDeletionTS || !sameFinalizers(finalizers, lastFinalizers)
			if stateChanged || time.Since(lastProgress) >= PollGoneProgressEvery {
				if deletionTS == "" {
					logger.Info("%s %s still alive (no deletionTimestamp yet, finalizers=%v)",
						resourceLabel, ref, finalizers)
				} else {
					logger.Info("%s %s still terminating (deletionTimestamp=%s, finalizers=%v)",
						resourceLabel, ref, deletionTS, finalizers)
				}
				lastProgress = time.Now()
				lastFinalizers = append(lastFinalizers[:0], finalizers...)
				lastDeletionTS = deletionTS
			}
		default:
			consecutiveErrs++
			if consecutiveErrs >= 3 {
				logger.Warn(
					"%s %s GET failed for %d consecutive iterations: %v",
					resourceLabel, ref, consecutiveErrs, err,
				)
			} else {
				logger.Debug("Error getting %s %s: %v", resourceLabel, ref, err)
			}
		}

		select {
		case <-deadlineCtx.Done():
			// Surface the last observed state in the timeout error so the
			// caller (and the dev reading the test log) can immediately tell
			// whether they're stuck on a finalizer, on a missing
			// deletionTimestamp, or on a network issue.
			lastSeen := "no observation yet"
			if lastDeletionTS != "" || len(lastFinalizers) > 0 {
				lastSeen = fmt.Sprintf("deletionTimestamp=%q, finalizers=%v", lastDeletionTS, lastFinalizers)
			}
			return fmt.Errorf("timeout waiting for %s %s to be gone (%s): %w",
				resourceLabel, ref, lastSeen, deadlineCtx.Err())
		case <-ticker.C:
		}
	}
}

// formatRef renders a resource reference as either "name" (cluster-scoped)
// or "namespace/name" (namespaced) for log lines and error messages.
func formatRef(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// errIfTerminating returns a descriptive error if obj has a non-nil
// metadata.deletionTimestamp. Used by Create* helpers to fail-fast in the
// IsAlreadyExists branch when an existing CR is in `Terminating` state —
// updating its spec would be a no-op (the controller is busy unwinding the
// finalizer), and a follow-up Wait*Ready would hang forever because phase
// transitions never reach a Ready state on a Terminating object.
//
// `kind` is the human-readable kind ("CephCluster") and `ref` is the
// formatted "[namespace/]name" identifier.
func errIfTerminating(obj *unstructured.Unstructured, kind, ref string) error {
	dt := obj.GetDeletionTimestamp()
	if dt == nil {
		return nil
	}
	return fmt.Errorf(
		"%s %s exists but is being deleted (deletionTimestamp=%s, finalizers=%v); "+
			"wait for it to disappear or remove finalizers manually before re-running",
		kind, ref, dt.Format(time.RFC3339), obj.GetFinalizers(),
	)
}

// sameFinalizers returns true when both slices contain the same strings in
// the same order. Used by pollResourceUntilGone to decide if the visible
// state has changed.
func sameFinalizers(a, b []string) bool {
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

// getWithTimeout wraps dynamicClient.Get with a per-call deadline derived
// from the parent context. The wrapper avoids leaking goroutines blocked on
// a dead TCP connection. An empty namespace selects the cluster-scoped
// path (used by csi-ceph CRs like CephClusterConnection).
func getWithTimeout(
	parent context.Context,
	dynamicClient dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	perCallTimeout time.Duration,
) (*unstructured.Unstructured, error) {
	callCtx, cancel := context.WithTimeout(parent, perCallTimeout)
	defer cancel()
	if namespace == "" {
		return dynamicClient.Resource(gvr).Get(callCtx, name, metav1.GetOptions{})
	}
	return dynamicClient.Resource(gvr).Namespace(namespace).Get(callCtx, name, metav1.GetOptions{})
}
