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

package kubernetes

import (
	"context"
	"fmt"
	"strings"

	"github.com/deckhouse/storage-e2e/internal/kubernetes/core"
	"k8s.io/client-go/rest"
)

// FindSecretByName finds a secret by name, trying multiple matching strategies
// This helps with issues where secret names might have hidden Unicode characters
// 1. Exact match
// 2. Case-insensitive match
// 3. Fuzzy match (ignoring common Unicode issues like non-breaking spaces)
// Returns the actual secret name found (which may differ from the requested name due to Unicode issues)
func FindSecretByName(ctx context.Context, kubeconfig *rest.Config, namespace, name string) (string, error) {
	secretClient, err := core.NewSecretClient(kubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to create secret client: %w", err)
	}

	// First try exact match
	secret, err := secretClient.Get(ctx, namespace, name)
	if err == nil {
		return secret.Name, nil
	}

	// If exact match fails, list all secrets and try to find a match
	secretList, err := secretClient.List(ctx, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to list secrets: %w", err)
	}

	// Normalize the search name: remove common problematic Unicode characters
	normalizedName := normalizeSecretName(name)

	// Try case-insensitive and normalized matching
	for i := range secretList.Items {
		secretName := secretList.Items[i].Name

		// Try exact case-insensitive match
		if strings.EqualFold(secretName, name) {
			return secretName, nil
		}

		// Try normalized match (handles hidden Unicode characters)
		if normalizeSecretName(secretName) == normalizedName {
			return secretName, nil
		}
	}

	// If still not found, return error with available secret names
	availableNames := make([]string, 0, len(secretList.Items))
	for _, s := range secretList.Items {
		availableNames = append(availableNames, s.Name)
	}
	return "", fmt.Errorf("secret %s/%s not found. Available secrets: %v", namespace, name, availableNames)
}

// GetSecretDataValue retrieves a specific data value from a secret by name
// It uses FindSecretByName to handle potential Unicode character issues
func GetSecretDataValue(ctx context.Context, kubeconfig *rest.Config, namespace, name, key string) (string, error) {
	actualName, err := FindSecretByName(ctx, kubeconfig, namespace, name)
	if err != nil {
		return "", err
	}

	secretClient, err := core.NewSecretClient(kubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to create secret client: %w", err)
	}

	return secretClient.GetDataValue(ctx, namespace, actualName, key)
}

// normalizeSecretName normalizes a secret name by removing/replacing problematic Unicode characters
// This helps match secrets that have hidden Unicode characters (like non-breaking spaces)
func normalizeSecretName(name string) string {
	// Replace common problematic Unicode characters with their ASCII equivalents
	normalized := strings.ReplaceAll(name, "\u00A0", " ")     // Non-breaking space -> regular space
	normalized = strings.ReplaceAll(normalized, "\u200B", "") // Zero-width space -> empty
	normalized = strings.ReplaceAll(normalized, "\uFEFF", "") // Zero-width no-break space -> empty
	normalized = strings.ReplaceAll(normalized, "\u200C", "") // Zero-width non-joiner -> empty
	normalized = strings.ReplaceAll(normalized, "\u200D", "") // Zero-width joiner -> empty
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	return normalized
}
