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

package dvp

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func secretObj(name string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: bootstrapSecretNamespace, Name: name}}
}

func nodeObj(name string, ready bool, labels map[string]string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func deckhouseDeployment(available bool) *appsv1.Deployment {
	status := corev1.ConditionFalse
	if available {
		status = corev1.ConditionTrue
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: deckhouseNamespace, Name: deckhouseDeploymentName},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: status}},
		},
	}
}

func TestWaitBootstrapSecretsPresent(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset(secretObj(masterBootstrapSecret), secretObj(workerBootstrapSecret))
	if err := waitBootstrapSecretsClient(context.Background(), cs, time.Second); err != nil {
		t.Fatalf("waitBootstrapSecretsClient() error = %v, want nil", err)
	}
}

func TestWaitBootstrapSecretsMissingTimesOut(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset(secretObj(masterBootstrapSecret)) // worker secret absent
	err := waitBootstrapSecretsClient(context.Background(), cs, 30*time.Millisecond)
	if err == nil {
		t.Fatal("waitBootstrapSecretsClient() error = nil, want timeout")
	}
}

func TestWaitExistingInstallReady(t *testing.T) {
	t.Parallel()
	cpLabel := map[string]string{controlPlaneNodeLabels[0]: ""}

	ready := fake.NewClientset(
		nodeObj("m1", true, cpLabel), deckhouseDeployment(true),
		secretObj(masterBootstrapSecret), secretObj(workerBootstrapSecret),
	)
	if err := waitExistingInstallReadyClient(context.Background(), ready, time.Second); err != nil {
		t.Fatalf("waitExistingInstallReadyClient() error = %v, want nil", err)
	}

	// Deckhouse deployment not Available → keeps polling until timeout.
	notReady := fake.NewClientset(
		nodeObj("m1", true, cpLabel), deckhouseDeployment(false),
		secretObj(masterBootstrapSecret), secretObj(workerBootstrapSecret),
	)
	if err := waitExistingInstallReadyClient(context.Background(), notReady, 30*time.Millisecond); err == nil {
		t.Fatal("waitExistingInstallReadyClient() error = nil, want timeout (deckhouse not Available)")
	}

	// Bootstrap secrets missing → not ready even with a healthy deployment.
	noSecrets := fake.NewClientset(nodeObj("m1", true, cpLabel), deckhouseDeployment(true))
	if err := waitExistingInstallReadyClient(context.Background(), noSecrets, 30*time.Millisecond); err == nil {
		t.Fatal("waitExistingInstallReadyClient() error = nil, want timeout (secrets absent)")
	}
}

func TestWaitNodesReady(t *testing.T) {
	t.Parallel()
	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{{Hostname: "m1"}},
		Workers: []config.ClusterNode{{Hostname: "w1"}},
	}

	cpLabel := map[string]string{controlPlaneNodeLabels[0]: ""}
	ready := fake.NewClientset(nodeObj("m1", true, cpLabel), nodeObj("w1", true, nil))
	if err := waitNodesReadyClient(context.Background(), ready, def, time.Second); err != nil {
		t.Fatalf("waitNodesReadyClient() error = %v, want nil", err)
	}

	notEnough := fake.NewClientset(nodeObj("m1", true, cpLabel), nodeObj("w1", false, nil))
	if err := waitNodesReadyClient(context.Background(), notEnough, def, 30*time.Millisecond); err == nil {
		t.Fatal("waitNodesReadyClient() error = nil, want timeout (only 1 of 2 Ready)")
	}
}

func TestCheckHealth(t *testing.T) {
	t.Parallel()
	cpLabel := map[string]string{controlPlaneNodeLabels[0]: ""}

	tests := []struct {
		name    string
		objs    []runtime.Object
		wantErr bool
	}{
		{
			name:    "healthy",
			objs:    []runtime.Object{nodeObj("m1", true, cpLabel), nodeObj("w1", true, nil), deckhouseDeployment(true)},
			wantErr: false,
		},
		{
			name:    "control-plane not ready",
			objs:    []runtime.Object{nodeObj("m1", false, cpLabel), deckhouseDeployment(true)},
			wantErr: true,
		},
		{
			name:    "no control-plane nodes",
			objs:    []runtime.Object{nodeObj("w1", true, nil), deckhouseDeployment(true)},
			wantErr: true,
		},
		{
			name:    "deckhouse deployment not available",
			objs:    []runtime.Object{nodeObj("m1", true, cpLabel), deckhouseDeployment(false)},
			wantErr: true,
		},
		{
			name:    "deckhouse deployment missing",
			objs:    []runtime.Object{nodeObj("m1", true, cpLabel)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cs := fake.NewClientset(tt.objs...)
			err := checkHealthClient(context.Background(), cs)
			if tt.wantErr && err == nil {
				t.Errorf("checkHealthClient() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("checkHealthClient() error = %v, want nil", err)
			}
		})
	}
}
