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
	"os"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/deckhouse/storage-e2e/pkg/retry"
)

// ApplyClient handles applying YAML manifests to a Kubernetes cluster
type ApplyClient struct {
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
}

// NewApplyClient creates a new ApplyClient
func NewApplyClient(config *rest.Config) (*ApplyClient, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	return &ApplyClient{
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
	}, nil
}

// ApplyYAML applies YAML manifest(s) to the cluster
// The yamlContent can contain multiple YAML documents separated by "---"
// namespace parameter is optional - if empty, uses namespace from manifest or "default"
func (c *ApplyClient) ApplyYAML(ctx context.Context, yamlContent string, namespace string) error {
	// Split YAML content by document separator
	documents := splitYAMLDocuments(yamlContent)

	var errs []error
	for i, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		if err := c.applyDocument(ctx, doc, namespace); err != nil {
			errs = append(errs, fmt.Errorf("document %d: %w", i+1, err))
		}
	}

	return errors.NewAggregate(errs)
}

// applyDocument applies a single YAML document
func (c *ApplyClient) applyDocument(ctx context.Context, yamlDoc string, defaultNamespace string) error {
	// Decode YAML to unstructured object
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}

	_, _, err := decoder.Decode([]byte(yamlDoc), nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	// Set namespace if not specified in manifest
	if obj.GetNamespace() == "" && defaultNamespace != "" {
		obj.SetNamespace(defaultNamespace)
	}

	// Get GVK
	gvk := obj.GroupVersionKind()
	if gvk.Empty() {
		return fmt.Errorf("GroupVersionKind is empty for object: %s", obj.GetName())
	}

	// Get REST mapping for the GVK
	groupResources, err := restmapper.GetAPIGroupResources(c.discoveryClient)
	if err != nil {
		return fmt.Errorf("failed to get API group resources: %w", err)
	}

	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk.String(), err)
	}

	// Get dynamic resource interface
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == "namespace" {
		// Namespaced resource
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		dr = c.dynamicClient.Resource(mapping.Resource).Namespace(ns)
	} else {
		// Cluster-scoped resource
		dr = c.dynamicClient.Resource(mapping.Resource)
	}

	// Try to get existing resource with retry for transient errors
	operationName := fmt.Sprintf("apply %s/%s", obj.GetKind(), obj.GetName())
	return retry.DoVoid(ctx, retry.DefaultConfig, operationName, func() error {
		existing, err := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err == nil {
			// Resource exists, update it
			obj.SetResourceVersion(existing.GetResourceVersion())
			_, err = dr.Update(ctx, obj, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update %s/%s: %w", obj.GetKind(), obj.GetName(), err)
			}
		} else {
			// Resource doesn't exist, create it
			_, err = dr.Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create %s/%s: %w", obj.GetKind(), obj.GetName(), err)
			}
		}
		return nil
	})
}

// CreateYAML creates resources from YAML manifest(s)
// Unlike ApplyYAML, this will fail if resources already exist
func (c *ApplyClient) CreateYAML(ctx context.Context, yamlContent string, namespace string) error {
	// Split YAML content by document separator
	documents := splitYAMLDocuments(yamlContent)

	var errs []error
	for i, doc := range documents {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		if err := c.createDocument(ctx, doc, namespace); err != nil {
			errs = append(errs, fmt.Errorf("document %d: %w", i+1, err))
		}
	}

	return errors.NewAggregate(errs)
}

// createDocument creates a single YAML document
func (c *ApplyClient) createDocument(ctx context.Context, yamlDoc string, defaultNamespace string) error {
	// Decode YAML to unstructured object
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}

	_, _, err := decoder.Decode([]byte(yamlDoc), nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	// Set namespace if not specified in manifest
	if obj.GetNamespace() == "" && defaultNamespace != "" {
		obj.SetNamespace(defaultNamespace)
	}

	// Get GVK
	gvk := obj.GroupVersionKind()
	if gvk.Empty() {
		return fmt.Errorf("GroupVersionKind is empty for object: %s", obj.GetName())
	}

	// Get REST mapping for the GVK
	groupResources, err := restmapper.GetAPIGroupResources(c.discoveryClient)
	if err != nil {
		return fmt.Errorf("failed to get API group resources: %w", err)
	}

	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk.String(), err)
	}

	// Get dynamic resource interface
	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == "namespace" {
		// Namespaced resource
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		dr = c.dynamicClient.Resource(mapping.Resource).Namespace(ns)
	} else {
		// Cluster-scoped resource
		dr = c.dynamicClient.Resource(mapping.Resource)
	}

	// Create resource with retry for transient errors
	operationName := fmt.Sprintf("create %s/%s", obj.GetKind(), obj.GetName())
	return retry.DoVoid(ctx, retry.DefaultConfig, operationName, func() error {
		_, err = dr.Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		return nil
	})
}

// CreateYAMLFromFileWithEnvvars reads a YAML file, validates environment variables, substitutes them, and creates resources
// Returns error if file cannot be read, any ${VAR} is not set, or resource creation fails
func (c *ApplyClient) CreateYAMLFromFileWithEnvvars(ctx context.Context, filePath string, namespace string) error {
	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	yamlContent := string(content)

	// Find all ${VAR} patterns and check if they're set
	unsetVars := FindUnsetEnvVars(yamlContent)
	if len(unsetVars) > 0 {
		return fmt.Errorf("environment variables not set: %v", unsetVars)
	}

	// Substitute environment variables
	expanded := os.ExpandEnv(yamlContent)

	// Create resources
	return c.CreateYAML(ctx, expanded, namespace)
}

// FindUnsetEnvVars finds all ${VAR} patterns in content and returns those that are not set
func FindUnsetEnvVars(content string) []string {
	// Match ${VAR} pattern
	re := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	matches := re.FindAllStringSubmatch(content, -1)

	seen := make(map[string]bool)
	var unset []string

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		varName := match[1]
		if seen[varName] {
			continue
		}
		seen[varName] = true

		if os.Getenv(varName) == "" {
			unset = append(unset, varName)
		}
	}

	return unset
}

// splitYAMLDocuments splits YAML content by "---" separator
func splitYAMLDocuments(yamlContent string) []string {
	// Split by document separator
	docs := strings.Split(yamlContent, "\n---\n")

	// Clean up each document
	var result []string
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc != "" && doc != "---" {
			result = append(result, doc)
		}
	}

	return result
}
