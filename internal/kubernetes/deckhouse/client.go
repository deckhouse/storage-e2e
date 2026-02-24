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

package deckhouse

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg/log"

	deckhousev1alpha1 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha1"
	deckhousev1alpha2 "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha2"
	"github.com/deckhouse/storage-e2e/pkg/retry"
)

var (
	loggerSetOnce sync.Once
)

// Client provides access to deckhouse resources
type Client struct {
	client client.Client
}

// NewClient creates a new deckhouse client from a rest.Config
// It uses controller-runtime client which provides type-safe access to CRDs.
// Includes retry logic for transient network errors during client creation,
// since controller-runtime client.New() performs API discovery which can fail
// with TLS handshake timeouts or other transient network issues.
func NewClient(ctx context.Context, config *rest.Config) (*Client, error) {
	// Initialize controller-runtime logger once to suppress warnings
	loggerSetOnce.Do(func() {
		// Use a null logger to suppress controller-runtime warnings
		// We use our own logger for application logging
		ctrl.SetLogger(logr.Discard())
	})

	return retry.Do(ctx, retry.DefaultConfig, "create deckhouse client", func() (*Client, error) {
		scheme := runtime.NewScheme()

		// Register deckhouse API types with the scheme
		if err := deckhousev1alpha1.SchemeBuilder.AddToScheme(scheme); err != nil {
			return nil, fmt.Errorf("failed to add deckhouse v1alpha1 scheme: %w", err)
		}
		if err := deckhousev1alpha2.SchemeBuilder.AddToScheme(scheme); err != nil {
			return nil, fmt.Errorf("failed to add deckhouse v1alpha2 scheme: %w", err)
		}

		cl, err := client.New(config, client.Options{Scheme: scheme})
		if err != nil {
			return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
		}

		return &Client{client: cl}, nil
	})
}
