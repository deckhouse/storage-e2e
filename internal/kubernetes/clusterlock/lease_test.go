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

package clusterlock

import (
	"context"
	"strings"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func foreignLease(renewedAt time.Time, durationSeconds int32) *coordinationv1.Lease {
	holder := "someone-else@other-host/42"
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      LeaseName,
			Namespace: Namespace,
			Annotations: map[string]string{
				annotationTestName: "other-test",
				annotationLockedBy: "someone-else",
				annotationHostname: "other-host",
				annotationPID:      "42",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &durationSeconds,
			AcquireTime:          &metav1.MicroTime{Time: renewedAt},
			RenewTime:            &metav1.MicroTime{Time: renewedAt},
		},
	}
}

func TestAcquireLeaseFresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	lock, err := acquireLease(ctx, clientset, "my-test", time.Hour)
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release(ctx) })

	lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if holderOf(lease) != lock.holder {
		t.Errorf("lease holder = %q, want %q", holderOf(lease), lock.holder)
	}
	if lease.Annotations[annotationTestName] != "my-test" {
		t.Errorf("test-name annotation = %q, want %q", lease.Annotations[annotationTestName], "my-test")
	}

	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	_, err = clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("lease should be deleted after Release, got err=%v", err)
	}
}

func TestAcquireLeaseHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientset := fake.NewSimpleClientset(foreignLease(time.Now(), 3600))

	_, err := acquireLease(ctx, clientset, "my-test", time.Hour)
	if err == nil {
		t.Fatal("acquireLease should fail when the lease is held and not expired")
	}
	if !strings.Contains(err.Error(), "already locked") {
		t.Errorf("error should mention 'already locked', got: %v", err)
	}
	if !strings.Contains(err.Error(), "other-test") {
		t.Errorf("error should mention the holder's test name, got: %v", err)
	}
}

func TestAcquireLeaseExpiredTakeover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Renewed two hours ago with a 60s duration — long expired.
	clientset := fake.NewSimpleClientset(foreignLease(time.Now().Add(-2*time.Hour), 60))

	lock, err := acquireLease(ctx, clientset, "my-test", time.Hour)
	if err != nil {
		t.Fatalf("acquireLease should take over an expired lease: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release(ctx) })

	lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if holderOf(lease) != lock.holder {
		t.Errorf("lease holder = %q, want takeover by %q", holderOf(lease), lock.holder)
	}
	if lease.Spec.LeaseTransitions == nil || *lease.Spec.LeaseTransitions != 1 {
		t.Errorf("lease transitions = %v, want 1", lease.Spec.LeaseTransitions)
	}
}

func TestReleaseDoesNotDeleteForeignLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	lock, err := acquireLease(ctx, clientset, "my-test", time.Hour)
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}

	// Simulate another run taking the lease over (e.g. after expiry).
	stolen := foreignLease(time.Now(), 3600)
	if _, err := clientset.CoordinationV1().Leases(Namespace).Update(ctx, stolen, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update lease: %v", err)
	}

	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("foreign lease should survive our Release: %v", err)
	}
	if holderOf(lease) != "someone-else@other-host/42" {
		t.Errorf("unexpected holder after Release: %q", holderOf(lease))
	}
}

func TestReleaseIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	lock, err := acquireLease(ctx, clientset, "my-test", time.Hour)
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("second Release should be a no-op: %v", err)
	}
}

func TestLeaseRenewal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	// 300ms duration → renewal ticks every 100ms.
	lock, err := acquireLease(ctx, clientset, "my-test", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release(ctx) })

	initial, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get lease: %v", err)
		}
		if lease.Spec.RenewTime.After(initial.Spec.RenewTime.Time) {
			return // renewed
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("lease renew time was not advanced by the background renewer")
}
