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

package commander

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func nodeWithAddresses(name string, addrs ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Addresses: addrs},
	}
}

func TestInternalIPResolver_PicksInternalIP(t *testing.T) {
	node := nodeWithAddresses("worker-1",
		corev1.NodeAddress{Type: corev1.NodeHostName, Address: "worker-1"},
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.7"},
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.12.4.151"},
	)
	r := &internalIPResolver{clientset: fake.NewSimpleClientset(node)}

	got, err := r.Resolve(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("Resolve returned unexpected error: %v", err)
	}
	if want := "10.12.4.151"; got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestInternalIPResolver_ErrorsWithoutInternalIP(t *testing.T) {
	node := nodeWithAddresses("worker-1",
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.7"},
	)
	r := &internalIPResolver{clientset: fake.NewSimpleClientset(node)}

	if _, err := r.Resolve(context.Background(), "worker-1"); err == nil {
		t.Fatal("expected an error for a node without an InternalIP, got nil")
	} else if !strings.Contains(err.Error(), "no InternalIP") {
		t.Errorf("error = %v, want it to mention the missing InternalIP", err)
	}
}

func TestInternalIPResolver_ErrorsOnMissingNode(t *testing.T) {
	r := &internalIPResolver{clientset: fake.NewSimpleClientset()}

	if _, err := r.Resolve(context.Background(), "absent"); err == nil {
		t.Fatal("expected an error for an unknown node, got nil")
	}
}

// resolverFunc lets a test stub address resolution.
type resolverFunc func(ctx context.Context, nodeName string) (string, error)

func (f resolverFunc) Resolve(ctx context.Context, nodeName string) (string, error) {
	return f(ctx, nodeName)
}

// A resolution failure must surface before any SSH is attempted, so the executor
// is usable (and its errors legible) without a reachable node.
func TestCommanderNodeExecutor_SurfacesResolveError(t *testing.T) {
	e := &commanderNodeExecutor{
		resolver: resolverFunc(func(context.Context, string) (string, error) {
			return "", context.DeadlineExceeded
		}),
	}

	if _, err := e.Exec(context.Background(), "worker-1", "true"); err == nil {
		t.Fatal("expected the resolve error to surface, got nil")
	}
}
