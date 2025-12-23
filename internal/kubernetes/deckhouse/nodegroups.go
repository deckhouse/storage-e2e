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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// NodeGroupGroupVersion is the API group and version for NodeGroup resources
	NodeGroupGroupVersion = "deckhouse.io/v1"
	// NodeGroupResource is the resource name for NodeGroup
	NodeGroupResource = "nodegroups"
)

var (
	// NodeGroupGVK is the GroupVersionKind for NodeGroup
	NodeGroupGVK = schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1",
		Kind:    "NodeGroup",
	}
)

// GetNodeGroup retrieves a NodeGroup by name
func GetNodeGroup(ctx context.Context, config *rest.Config, name string) (*unstructured.Unstructured, error) {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	nodeGroup := &unstructured.Unstructured{}
	nodeGroup.SetGroupVersionKind(NodeGroupGVK)
	key := client.ObjectKey{Name: name}
	if err := cl.client.Get(ctx, key, nodeGroup); err != nil {
		return nil, fmt.Errorf("failed to get nodegroup %s: %w", name, err)
	}

	return nodeGroup, nil
}

// CreateNodeGroup creates a NodeGroup resource
func CreateNodeGroup(ctx context.Context, config *rest.Config, name string, nodeType string) error {
	cl, err := NewClient(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create deckhouse client: %w", err)
	}

	nodeGroup := &unstructured.Unstructured{}
	nodeGroup.SetGroupVersionKind(NodeGroupGVK)
	nodeGroup.SetName(name)
	nodeGroup.Object["spec"] = map[string]interface{}{
		"nodeType": nodeType,
	}

	if err := cl.client.Create(ctx, nodeGroup); err != nil {
		return fmt.Errorf("failed to create nodegroup %s: %w", name, err)
	}

	return nil
}
