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

package commander

import "errors"

var (
	// ErrClusterNotFound indicates the cluster was not found
	ErrClusterNotFound = errors.New("cluster not found")

	// ErrTemplateNotFound indicates the template was not found
	ErrTemplateNotFound = errors.New("template not found")

	// ErrClusterNotReady indicates the cluster is not in ready state
	ErrClusterNotReady = errors.New("cluster is not ready")

	// ErrKubeconfigNotAvailable indicates kubeconfig is not yet available
	ErrKubeconfigNotAvailable = errors.New("kubeconfig is not available")
)
