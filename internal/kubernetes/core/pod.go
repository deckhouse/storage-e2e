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

// PodClient provides operations on Pod resources
type PodClient struct {
	client kubernetes.Interface
}

// NewPodClient creates a new pod client from a rest.Config
func NewPodClient(config *rest.Config) (*PodClient, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return &PodClient{client: clientset}, nil
}

// ListByLabelSelector lists pods in a namespace matching the label selector
func (c *PodClient) ListByLabelSelector(ctx context.Context, namespace, labelSelector string) (*corev1.PodList, error) {
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s with selector %s: %w", namespace, labelSelector, err)
	}
	return pods, nil
}

// ListAll lists all pods in a namespace
func (c *PodClient) ListAll(ctx context.Context, namespace string) (*corev1.PodList, error) {
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
	}
	return pods, nil
}

// IsRunning checks if a pod is in Running phase
func (c *PodClient) IsRunning(ctx context.Context, pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning
}

// AllContainersReady checks if all containers in a pod are ready
func (c *PodClient) AllContainersReady(ctx context.Context, pod *corev1.Pod) bool {
	if len(pod.Spec.Containers) == 0 {
		return false
	}
	if len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return false
		}
	}
	return true
}
