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

package storage

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

// BlockDeviceInfo represents a simplified block device information
type BlockDeviceInfo struct {
	Name     string
	NodeName string
	Path     string
	Size     string
}

// BlockDeviceClient provides operations on BlockDevice resources
type BlockDeviceClient struct {
	client client.Client
}

// NewBlockDeviceClient creates a new BlockDevice client from a rest.Config
// It uses controller-runtime client which provides type-safe access to CRDs
func NewBlockDeviceClient(ctx context.Context, config *rest.Config) (*BlockDeviceClient, error) {
	scheme := runtime.NewScheme()

	// Register sds-node-configurator API types with the scheme
	if err := snc.AddToScheme(scheme); err != nil {
		return nil, err
	}

	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	return &BlockDeviceClient{client: cl}, nil
}

// List lists all BlockDevices in the cluster
func (c *BlockDeviceClient) List(ctx context.Context) (*snc.BlockDeviceList, error) {
	var bdList snc.BlockDeviceList
	if err := c.client.List(ctx, &bdList); err != nil {
		return nil, fmt.Errorf("failed to list BlockDevices: %w", err)
	}
	return &bdList, nil
}

// Get retrieves a BlockDevice by name
func (c *BlockDeviceClient) Get(ctx context.Context, name string) (*snc.BlockDevice, error) {
	var bd snc.BlockDevice
	if err := c.client.Get(ctx, client.ObjectKey{Name: name}, &bd); err != nil {
		return nil, fmt.Errorf("failed to get BlockDevice %s: %w", name, err)
	}
	return &bd, nil
}

// ListConsumable returns all consumable BlockDevices
func (c *BlockDeviceClient) ListConsumable(ctx context.Context) ([]BlockDeviceInfo, error) {
	bdList, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	var result []BlockDeviceInfo
	for _, bd := range bdList.Items {
		if !bd.Status.Consumable {
			continue
		}

		result = append(result, BlockDeviceInfo{
			Name:     bd.Name,
			NodeName: bd.Status.NodeName,
			Path:     bd.Status.Path,
			Size:     bd.Status.Size.String(),
		})
	}

	return result, nil
}

// ListByNode returns all BlockDevices on a specific node
func (c *BlockDeviceClient) ListByNode(ctx context.Context, nodeName string) ([]BlockDeviceInfo, error) {
	bdList, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	var result []BlockDeviceInfo
	for _, bd := range bdList.Items {
		if bd.Status.NodeName != nodeName {
			continue
		}

		result = append(result, BlockDeviceInfo{
			Name:     bd.Name,
			NodeName: bd.Status.NodeName,
			Path:     bd.Status.Path,
			Size:     bd.Status.Size.String(),
		})
	}

	return result, nil
}

// ListConsumableByNode returns all consumable BlockDevices on a specific node
func (c *BlockDeviceClient) ListConsumableByNode(ctx context.Context, nodeName string) ([]BlockDeviceInfo, error) {
	bdList, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	var result []BlockDeviceInfo
	for _, bd := range bdList.Items {
		if bd.Status.NodeName != nodeName || !bd.Status.Consumable {
			continue
		}

		result = append(result, BlockDeviceInfo{
			Name:     bd.Name,
			NodeName: bd.Status.NodeName,
			Path:     bd.Status.Path,
			Size:     bd.Status.Size.String(),
		})
	}

	return result, nil
}
