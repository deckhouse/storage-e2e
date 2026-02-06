/*
Copyright 2025 Flant JSC

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

package cluster

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/logger"
	k8sutils "github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

const (
	// ClusterLockNamespace is the namespace where the cluster lock ConfigMap is created
	ClusterLockNamespace = "default"
	// ClusterLockConfigMapName is the name of the ConfigMap used to lock the cluster
	ClusterLockConfigMapName = "e2e-cluster-lock"

	// ConfigMap data keys
	lockKeyTestName = "test-name"
	lockKeyLockedAt = "locked-at"
	lockKeyLockedBy = "locked-by"
	lockKeyHostname = "hostname"
	lockKeyPID      = "pid"
)

// ClusterLockInfo contains information about who locked the cluster
type ClusterLockInfo struct {
	TestName string
	LockedAt time.Time
	LockedBy string
	Hostname string
	PID      int
}

// AcquireClusterLock creates a ConfigMap in the default namespace to indicate the cluster is busy.
// If the cluster is already locked, it returns an error with information about who holds the lock.
// Uses retry logic for transient network errors.
func AcquireClusterLock(ctx context.Context, kubeconfig *rest.Config, testName string) error {
	clientset, err := k8sutils.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	// Get current user and hostname for lock info
	hostname, _ := os.Hostname()
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		currentUser = os.Getenv("USERNAME") // Windows fallback
	}
	if currentUser == "" {
		currentUser = "unknown"
	}

	// Create the lock ConfigMap
	lockConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClusterLockConfigMapName,
			Namespace: ClusterLockNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "storage-e2e",
				"app.kubernetes.io/component":  "cluster-lock",
			},
		},
		Data: map[string]string{
			lockKeyTestName: testName,
			lockKeyLockedAt: time.Now().UTC().Format(time.RFC3339),
			lockKeyLockedBy: currentUser,
			lockKeyHostname: hostname,
			lockKeyPID:      fmt.Sprintf("%d", os.Getpid()),
		},
	}

	// Use retry for transient network errors when checking and creating lock
	return retry.DoVoid(ctx, retry.DefaultConfig, "acquire cluster lock", func() error {
		// Check if lock already exists
		_, err := clientset.CoreV1().ConfigMaps(ClusterLockNamespace).Get(ctx, ClusterLockConfigMapName, metav1.GetOptions{})
		if err == nil {
			// Lock exists, get lock info to provide helpful error message
			// This is a permanent error (cluster is locked), not retryable
			lockInfo, infoErr := GetClusterLockInfo(ctx, kubeconfig)
			if infoErr != nil {
				// Return non-retryable error
				return fmt.Errorf("cluster is already locked by another test (could not retrieve lock details: %v)", infoErr)
			}
			// Return non-retryable error
			return fmt.Errorf("cluster is already locked: test=%s, locked_at=%s, locked_by=%s, hostname=%s, pid=%d",
				lockInfo.TestName, lockInfo.LockedAt.Format(time.RFC3339), lockInfo.LockedBy, lockInfo.Hostname, lockInfo.PID)
		}
		if !errors.IsNotFound(err) {
			// This might be a transient error (network issue)
			return fmt.Errorf("failed to check for existing cluster lock: %w", err)
		}

		// Lock doesn't exist, try to create it
		_, err = clientset.CoreV1().ConfigMaps(ClusterLockNamespace).Create(ctx, lockConfigMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create cluster lock: %w", err)
		}

		logger.Info("Cluster lock acquired: test=%s, hostname=%s, pid=%d", testName, hostname, os.Getpid())
		return nil
	})
}

// ReleaseClusterLock removes the cluster lock ConfigMap.
// It is safe to call even if the lock doesn't exist (no error will be returned).
// Uses retry logic for transient network errors.
func ReleaseClusterLock(ctx context.Context, kubeconfig *rest.Config) error {
	clientset, err := k8sutils.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return retry.DoVoid(ctx, retry.DefaultConfig, "release cluster lock", func() error {
		err := clientset.CoreV1().ConfigMaps(ClusterLockNamespace).Delete(ctx, ClusterLockConfigMapName, metav1.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return nil // Already deleted, not an error
			}
			return fmt.Errorf("failed to release cluster lock: %w", err)
		}

		logger.Info("Cluster lock released")
		return nil
	})
}

// IsClusterLocked checks if the cluster is currently locked by checking for the lock ConfigMap.
func IsClusterLocked(ctx context.Context, kubeconfig *rest.Config) (bool, error) {
	clientset, err := k8sutils.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	_, err = clientset.CoreV1().ConfigMaps(ClusterLockNamespace).Get(ctx, ClusterLockConfigMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check cluster lock: %w", err)
	}
	return true, nil
}

// GetClusterLockInfo retrieves information about the current cluster lock.
// Returns an error if the cluster is not locked.
func GetClusterLockInfo(ctx context.Context, kubeconfig *rest.Config) (*ClusterLockInfo, error) {
	clientset, err := k8sutils.NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	cm, err := clientset.CoreV1().ConfigMaps(ClusterLockNamespace).Get(ctx, ClusterLockConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("cluster is not locked or lock info unavailable: %w", err)
	}

	lockedAt, _ := time.Parse(time.RFC3339, cm.Data[lockKeyLockedAt])
	pid := 0
	if pidStr, ok := cm.Data[lockKeyPID]; ok {
		fmt.Sscanf(pidStr, "%d", &pid)
	}

	return &ClusterLockInfo{
		TestName: cm.Data[lockKeyTestName],
		LockedAt: lockedAt,
		LockedBy: cm.Data[lockKeyLockedBy],
		Hostname: cm.Data[lockKeyHostname],
		PID:      pid,
	}, nil
}

// ForceReleaseClusterLock forcefully removes the cluster lock.
// Use with caution - this should only be used for cleanup when you're sure no other test is running.
func ForceReleaseClusterLock(ctx context.Context, kubeconfig *rest.Config) error {
	logger.Warn("Force releasing cluster lock")
	return ReleaseClusterLock(ctx, kubeconfig)
}
