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

// Package clusterlock implements the coordination.k8s.io/v1 Lease-based
// cluster lock used by the pkg/e2e SDK.
package clusterlock

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	k8sutils "github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

const (
	Namespace = "default"
	LeaseName = "e2e-cluster-lock"

	// Without renewal the lock self-expires after this duration, so a lease
	// left behind by a dead run can be taken over.
	DefaultLeaseDuration = 5 * time.Minute

	annotationTestName = "storage-e2e.deckhouse.io/test-name"
	annotationLockedBy = "storage-e2e.deckhouse.io/locked-by"
	annotationHostname = "storage-e2e.deckhouse.io/hostname"
	annotationPID      = "storage-e2e.deckhouse.io/pid"

	renewRequestTimeout = 30 * time.Second
)

// LeaseLock is a held cluster lock, renewed in the background until Release.
type LeaseLock struct {
	clientset kubernetes.Interface
	holder    string
	duration  time.Duration

	cancelRenew context.CancelFunc
	renewDone   chan struct{}

	releaseOnce sync.Once
	releaseErr  error
}

// AcquireLease acquires the cluster lock, taking over an expired lease.
func AcquireLease(ctx context.Context, kubeconfig *rest.Config, testName string) (*LeaseLock, error) {
	clientset, err := k8sutils.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return acquireLease(ctx, clientset, testName, DefaultLeaseDuration)
}

func acquireLease(ctx context.Context, clientset kubernetes.Interface, testName string, duration time.Duration) (*LeaseLock, error) {
	holder := holderIdentity()

	err := retry.DoVoid(ctx, retry.DefaultConfig, "acquire cluster lease lock", func() error {
		return tryAcquireLease(ctx, clientset, holder, testName, duration)
	})
	if err != nil {
		return nil, err
	}

	logger.Info("Cluster lease lock acquired: lease=%s, holder=%s, test=%s", LeaseName, holder, testName)

	renewCtx, cancel := context.WithCancel(context.Background())
	lock := &LeaseLock{
		clientset:   clientset,
		holder:      holder,
		duration:    duration,
		cancelRenew: cancel,
		renewDone:   make(chan struct{}),
	}
	go lock.renewLoop(renewCtx)
	return lock, nil
}

func tryAcquireLease(ctx context.Context, clientset kubernetes.Interface, holder, testName string, duration time.Duration) error {
	now := time.Now()

	lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, createErr := clientset.CoordinationV1().Leases(Namespace).Create(ctx, newLease(holder, testName, duration, now), metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(createErr) {
			return lockedError(ctx, clientset)
		}
		if createErr != nil {
			return fmt.Errorf("failed to create cluster lease lock: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check for existing cluster lease lock: %w", err)
	}

	if !leaseExpired(lease, now, duration) {
		return leaseHeldError(lease)
	}

	logger.Warn("Taking over expired cluster lease lock (previous holder: %s)", holderOf(lease))
	transitions := int32(1)
	if lease.Spec.LeaseTransitions != nil {
		transitions = *lease.Spec.LeaseTransitions + 1
	}
	updated := lease.DeepCopy()
	applyLeaseSpec(updated, holder, testName, duration, now)
	updated.Spec.LeaseTransitions = &transitions

	if _, err := clientset.CoordinationV1().Leases(Namespace).Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsConflict(err) {
			return lockedError(ctx, clientset)
		}
		return fmt.Errorf("failed to take over expired cluster lease lock: %w", err)
	}
	return nil
}

// Release stops the renewal and deletes the lease if still held by us. Idempotent.
func (l *LeaseLock) Release(ctx context.Context) error {
	l.releaseOnce.Do(func() {
		l.cancelRenew()
		<-l.renewDone

		l.releaseErr = retry.DoVoid(ctx, retry.DefaultConfig, "release cluster lease lock", func() error {
			lease, err := l.clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to check cluster lease lock before release: %w", err)
			}
			if holderOf(lease) != l.holder {
				logger.Warn("Not releasing cluster lease lock: held by %s, not by us (%s)", holderOf(lease), l.holder)
				return nil
			}

			// UID precondition: don't delete a lease recreated by another run
			// between the Get and the Delete.
			err = l.clientset.CoordinationV1().Leases(Namespace).Delete(ctx, LeaseName, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &lease.UID},
			})
			if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to release cluster lease lock: %w", err)
			}
			logger.Info("Cluster lease lock released")
			return nil
		})
	})
	return l.releaseErr
}

func (l *LeaseLock) renewLoop(ctx context.Context) {
	defer close(l.renewDone)

	ticker := time.NewTicker(l.duration / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.renew(ctx)
		}
	}
}

func (l *LeaseLock) renew(ctx context.Context) {
	renewCtx, cancel := context.WithTimeout(ctx, renewRequestTimeout)
	defer cancel()

	lease, err := l.clientset.CoordinationV1().Leases(Namespace).Get(renewCtx, LeaseName, metav1.GetOptions{})
	if err != nil {
		logger.Warn("Failed to renew cluster lease lock (get): %v", err)
		return
	}
	if holderOf(lease) != l.holder {
		logger.Warn("Cluster lease lock is no longer held by us (holder: %s); skipping renewal", holderOf(lease))
		return
	}

	updated := lease.DeepCopy()
	updated.Spec.RenewTime = &metav1.MicroTime{Time: time.Now()}
	if _, err := l.clientset.CoordinationV1().Leases(Namespace).Update(renewCtx, updated, metav1.UpdateOptions{}); err != nil {
		logger.Warn("Failed to renew cluster lease lock (update): %v", err)
	}
}

func newLease(holder, testName string, duration time.Duration, now time.Time) *coordinationv1.Lease {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      LeaseName,
			Namespace: Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "storage-e2e",
				"app.kubernetes.io/component":  "cluster-lock",
			},
		},
	}
	applyLeaseSpec(lease, holder, testName, duration, now)
	return lease
}

func applyLeaseSpec(lease *coordinationv1.Lease, holder, testName string, duration time.Duration, now time.Time) {
	durationSeconds := int32(duration / time.Second)
	lease.Spec.HolderIdentity = &holder
	lease.Spec.LeaseDurationSeconds = &durationSeconds
	lease.Spec.AcquireTime = &metav1.MicroTime{Time: now}
	lease.Spec.RenewTime = &metav1.MicroTime{Time: now}

	hostname, _ := os.Hostname()
	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[annotationTestName] = testName
	lease.Annotations[annotationLockedBy] = currentUser()
	lease.Annotations[annotationHostname] = hostname
	lease.Annotations[annotationPID] = strconv.Itoa(os.Getpid())
}

func leaseExpired(lease *coordinationv1.Lease, now time.Time, fallbackDuration time.Duration) bool {
	renew := lease.Spec.RenewTime
	if renew == nil {
		renew = lease.Spec.AcquireTime
	}
	if renew == nil {
		return true // malformed lease without timestamps — take over
	}
	duration := fallbackDuration
	if lease.Spec.LeaseDurationSeconds != nil {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	return now.After(renew.Add(duration))
}

func leaseHeldError(lease *coordinationv1.Lease) error {
	renewedAt := ""
	if lease.Spec.RenewTime != nil {
		renewedAt = lease.Spec.RenewTime.Format(time.RFC3339)
	}
	return fmt.Errorf("cluster is already locked (lease %s/%s): holder=%s, test=%s, locked_by=%s, hostname=%s, pid=%s, renewed_at=%s",
		Namespace, LeaseName, holderOf(lease),
		lease.Annotations[annotationTestName], lease.Annotations[annotationLockedBy],
		lease.Annotations[annotationHostname], lease.Annotations[annotationPID],
		renewedAt)
}

func lockedError(ctx context.Context, clientset kubernetes.Interface) error {
	lease, err := clientset.CoordinationV1().Leases(Namespace).Get(ctx, LeaseName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cluster is already locked by another test (could not retrieve lease details: %v)", err)
	}
	return leaseHeldError(lease)
}

func holderOf(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

func holderIdentity() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s@%s/%d", currentUser(), hostname, os.Getpid())
}

func currentUser() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows fallback
	}
	if user == "" {
		user = "unknown"
	}
	return user
}
