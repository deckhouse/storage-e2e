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

package virtualization

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretClient provides operations on Secret resources for cloud-init
type SecretClient struct {
	client client.Client
}

// Get retrieves a Secret by namespace and name
func (c *SecretClient) Get(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// Create creates a new Secret
func (c *SecretClient) Create(ctx context.Context, secret *corev1.Secret) error {
	return c.client.Create(ctx, secret)
}

// Delete deletes a Secret
func (c *SecretClient) Delete(ctx context.Context, namespace, name string) error {
	secret := &corev1.Secret{}
	secret.Name = name
	secret.Namespace = namespace
	return c.client.Delete(ctx, secret)
}
