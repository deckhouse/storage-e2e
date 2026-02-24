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

// Package retry provides centralized retry logic for transient network errors
// in both Kubernetes API calls and SSH operations.
package retry

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// Config defines retry behavior parameters
type Config struct {
	// MaxRetries is the maximum number of retry attempts (0 means no retries, just one attempt)
	MaxRetries int
	// InitialWait is the initial wait duration before the first retry
	InitialWait time.Duration
	// MaxWait is the maximum wait duration between retries
	MaxWait time.Duration
	// Backoff is the multiplier applied to wait duration after each retry
	Backoff float64
	// LogRetries enables logging of retry attempts
	LogRetries bool
}

// DefaultConfig provides sensible defaults for Kubernetes API operations
var DefaultConfig = Config{
	MaxRetries:  10,
	InitialWait: 2 * time.Second,
	MaxWait:     30 * time.Second,
	Backoff:     1.5,
	LogRetries:  true,
}

// SSHConfig provides retry configuration optimized for SSH operations
// using centralized SSH retry configuration from config package
var SSHConfig = Config{
	MaxRetries:  config.SSHRetryCount,
	InitialWait: config.SSHRetryInitialDelay,
	MaxWait:     config.SSHRetryMaxDelay,
	Backoff:     2.0,
	LogRetries:  true,
}

// QuickConfig provides faster retry for operations expected to recover quickly
var QuickConfig = Config{
	MaxRetries:  3,
	InitialWait: 1 * time.Second,
	MaxWait:     5 * time.Second,
	Backoff:     2.0,
	LogRetries:  true,
}

// Do executes fn with retry logic for transient errors.
// It returns the result of fn and any error that occurred.
// If fn returns a non-retryable error, Do returns immediately without retrying.
func Do[T any](ctx context.Context, cfg Config, operationName string, fn func() (T, error)) (T, error) {
	var result T
	var lastErr error
	wait := cfg.InitialWait

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// Check context before attempting
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		result, lastErr = fn()
		if lastErr == nil {
			if attempt > 0 && cfg.LogRetries {
				logger.Debug("Operation '%s' succeeded after %d retries", operationName, attempt)
			}
			return result, nil
		}

		// Check if error is retryable
		if !IsRetryable(lastErr) {
			return result, lastErr
		}

		// Don't wait after the last attempt
		if attempt < cfg.MaxRetries {
			if cfg.LogRetries {
				logger.Warn("Retryable error in '%s' (attempt %d/%d): %v. Retrying in %v...",
					operationName, attempt+1, cfg.MaxRetries+1, lastErr, wait)
			}

			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(wait):
				// Apply exponential backoff
				wait = time.Duration(float64(wait) * cfg.Backoff)
				if wait > cfg.MaxWait {
					wait = cfg.MaxWait
				}
			}
		}
	}

	if cfg.LogRetries {
		logger.Error("Operation '%s' failed after %d attempts: %v", operationName, cfg.MaxRetries+1, lastErr)
	}
	return result, lastErr
}

// DoVoid is like Do but for functions that return only an error (no result value).
func DoVoid(ctx context.Context, cfg Config, operationName string, fn func() error) error {
	_, err := Do(ctx, cfg, operationName, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

// IsRetryable checks if an error is transient and should be retried.
// It handles both Kubernetes API errors and general network/SSH errors.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for Kubernetes API status errors using type assertion
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		code := statusErr.Status().Code
		// Retry on 5xx errors (except 501 Not Implemented) and 429 Too Many Requests
		if (code >= 500 && code != 501) || code == 429 {
			return true
		}
		// Check RetryAfterSeconds hint from server
		if statusErr.Status().Details != nil && statusErr.Status().Details.RetryAfterSeconds > 0 {
			return true
		}
	}

	// Check using Kubernetes API error helper functions
	if apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsTimeout(err) ||
		apierrors.IsInternalError(err) {
		return true
	}

	// Check for EOF errors (common in broken connections)
	if errors.Is(err, io.EOF) {
		return true
	}

	// String-based checks for transport-level and SSH errors
	errStr := err.Error()

	// Network/Transport errors
	networkPatterns := []string{
		"TLS handshake timeout",
		"connection refused",
		"connection reset",
		"connection timed out",
		"i/o timeout",
		"EOF",
		"broken pipe",
		"no route to host",
		"network is unreachable",
		"net/http: request canceled",
		"context deadline exceeded",
	}

	// Kubernetes API server errors
	k8sPatterns := []string{
		"server is currently unable to handle the request",
		"ServiceUnavailable",
		"the server is currently unable to handle the request",
		"etcdserver: request timed out",
		"etcdserver: leader changed",
		"failed to get server groups",
	}

	// SSH specific errors
	sshPatterns := []string{
		"failed to create SSH session",
		"ssh: handshake failed",
		"ssh: unable to authenticate",
		"ssh: connection lost",
		"failed to dial",
		"use of closed network connection",
	}

	// Webhook errors (may be temporary during pod restarts)
	webhookPatterns := []string{
		"failed calling webhook",
		"webhook",
	}

	// Combine all patterns
	allPatterns := make([]string, 0, len(networkPatterns)+len(k8sPatterns)+len(sshPatterns)+len(webhookPatterns))
	allPatterns = append(allPatterns, networkPatterns...)
	allPatterns = append(allPatterns, k8sPatterns...)
	allPatterns = append(allPatterns, sshPatterns...)
	allPatterns = append(allPatterns, webhookPatterns...)

	for _, pattern := range allPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// IsSSHConnectionError checks if an error specifically indicates SSH connection failure
// that would require reconnection (as opposed to command execution failure)
func IsSSHConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	connectionPatterns := []string{
		"failed to create SSH session",
		"ssh: handshake failed",
		"ssh: connection lost",
		"use of closed network connection",
		"connection refused",
		"connection reset",
		"broken pipe",
		"EOF",
		"i/o timeout",
	}

	for _, pattern := range connectionPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return errors.Is(err, io.EOF)
}

// WithRetryAfter returns a modified config that respects the RetryAfterSeconds hint
// from Kubernetes API errors. If the error contains a RetryAfterSeconds value,
// it will use that as the initial wait time.
func WithRetryAfter(cfg Config, err error) Config {
	if err == nil {
		return cfg
	}

	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		if statusErr.Status().Details != nil && statusErr.Status().Details.RetryAfterSeconds > 0 {
			retryAfter := time.Duration(statusErr.Status().Details.RetryAfterSeconds) * time.Second
			if retryAfter > cfg.InitialWait {
				cfg.InitialWait = retryAfter
			}
		}
	}

	return cfg
}
