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

package core

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SecretClient provides operations on Secret resources
type SecretClient struct {
	client kubernetes.Interface
}

// NewSecretClient creates a new secret client from a rest.Config
func NewSecretClient(config *rest.Config) (*SecretClient, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return &SecretClient{client: clientset}, nil
}

// Get retrieves a Secret by namespace and name
func (c *SecretClient) Get(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret, err := c.client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
	}
	return secret, nil
}

// List lists all Secrets in a namespace
func (c *SecretClient) List(ctx context.Context, namespace string) (*corev1.SecretList, error) {
	secrets, err := c.client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets in namespace %s: %w", namespace, err)
	}
	return secrets, nil
}

// GetDataValue retrieves a specific data value from a secret
// Note: Kubernetes secret.Data is already base64 decoded, so we return it directly
func (c *SecretClient) GetDataValue(ctx context.Context, namespace, name, key string) (string, error) {
	secret, err := c.Get(ctx, namespace, name)
	if err != nil {
		return "", err
	}

	value, exists := secret.Data[key]
	if !exists {
		return "", fmt.Errorf("key %s not found in secret %s/%s", key, namespace, name)
	}

	// Kubernetes secret.Data is already decoded from base64
	return string(value), nil
}
