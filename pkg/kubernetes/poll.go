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
	if namespace == "" || name == "" {
		return fmt.Errorf("namespace and name are required")
	}
	if isReady == nil {
		return fmt.Errorf("isReady is required")
	}
	if tickInterval <= 0 {
		tickInterval = PollTickInterval
	}

	logger.Debug("Waiting for %s %s/%s to become Ready (timeout: %v)", resourceLabel, namespace, name, readyTimeout)

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
			if ready, reason := isReady(obj); ready {
				if reason != "" {
					logger.Success("%s %s/%s is Ready (%s)", resourceLabel, namespace, name, reason)
				} else {
					logger.Success("%s %s/%s is Ready", resourceLabel, namespace, name)
				}
				return nil
			}
		case apierrors.IsNotFound(err):
			// Resource hasn't propagated yet. Treat as "still progressing"
			// without warning so we don't spam logs on healthy clusters that
			// just haven't observed the create yet.
			consecutiveErrs = 0
			logger.Debug("%s %s/%s not found yet", resourceLabel, namespace, name)
		default:
			consecutiveErrs++
			// Quiet the first two failures (spurious 5xx, leader re-election),
			// loud after that. Loud == WARN at every iteration so the user
			// can see the cluster connection is dying instead of waiting for
			// the readyTimeout to fire.
			if consecutiveErrs >= 3 {
				logger.Warn(
					"%s %s/%s GET failed for %d consecutive iterations: %v",
					resourceLabel, namespace, name, consecutiveErrs, err,
				)
			} else {
				logger.Debug("Error getting %s %s/%s: %v", resourceLabel, namespace, name, err)
			}
		}

		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timeout waiting for %s %s/%s: %w", resourceLabel, namespace, name, deadlineCtx.Err())
		case <-ticker.C:
		}
	}
}

// getWithTimeout wraps dynamicClient.Get with a per-call deadline derived
// from the parent context. The wrapper avoids leaking goroutines blocked on
// a dead TCP connection.
func getWithTimeout(
	parent context.Context,
	dynamicClient dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	perCallTimeout time.Duration,
) (*unstructured.Unstructured, error) {
	callCtx, cancel := context.WithTimeout(parent, perCallTimeout)
	defer cancel()
	return dynamicClient.Resource(gvr).Namespace(namespace).Get(callCtx, name, metav1.GetOptions{})
}
