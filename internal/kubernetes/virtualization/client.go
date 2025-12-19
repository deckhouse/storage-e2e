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

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client provides access to virtualization resources
type Client struct {
	client client.Client
}

// NewClient creates a new virtualization client from a rest.Config
// It uses controller-runtime client which provides type-safe access to CRDs
func NewClient(ctx context.Context, config *rest.Config) (*Client, error) {
	scheme := runtime.NewScheme()

	// Register virtualization API types with the scheme
	if err := v1alpha2.SchemeBuilder.AddToScheme(scheme); err != nil {
		return nil, err
	}

	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	return &Client{client: cl}, nil
}

// VirtualMachines returns a VirtualMachine client
func (c *Client) VirtualMachines() *VirtualMachineClient {
	return &VirtualMachineClient{client: c.client}
}

// VirtualDisks returns a VirtualDisk client
func (c *Client) VirtualDisks() *VirtualDiskClient {
	return &VirtualDiskClient{client: c.client}
}

// ClusterVirtualImages returns a ClusterVirtualImage client
func (c *Client) ClusterVirtualImages() *ClusterVirtualImageClient {
	return &ClusterVirtualImageClient{client: c.client}
}

// VirtualImages returns a VirtualImage client
func (c *Client) VirtualImages() *VirtualImageClient {
	return &VirtualImageClient{client: c.client}
}

// VirtualMachineBlockDeviceAttachments returns a VMBD client
func (c *Client) VirtualMachineBlockDeviceAttachments() *VMBDClient {
	return &VMBDClient{client: c.client}
}
