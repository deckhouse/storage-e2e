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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GetVMPodNodeAndContainerID returns the base cluster node name and the first container ID
// for the Pod that runs the given VM (e.g. virt-launcher-<vmName>-*).
// Used to run nsenter into the VM container from the base cluster node.
func GetVMPodNodeAndContainerID(ctx context.Context, baseConfig *rest.Config, namespace, vmName string) (nodeName, containerID string, err error) {
	clientset, err := kubernetes.NewForConfig(baseConfig)
	if err != nil {
		return "", "", fmt.Errorf("create clientset: %w", err)
	}
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", "", fmt.Errorf("list pods in %s: %w", namespace, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		// Match Pod that runs this VM: name often contains VM name (e.g. virt-launcher-master-1-xxx)
		if !strings.Contains(pod.Name, vmName) {
			continue
		}
		if pod.Spec.NodeName == "" {
			continue
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.ContainerID != "" {
				return pod.Spec.NodeName, cs.ContainerID, nil
			}
		}
		return "", "", fmt.Errorf("pod %s/%s has no container ID yet", namespace, pod.Name)
	}
	return "", "", fmt.Errorf("no running pod for VM %s in namespace %s", vmName, namespace)
}

// GetNodeSSHAddress returns an address (IP or hostname) suitable for SSH from the jump host to the given node.
// It prefers InternalIP, then ExternalIP, so that the jump host can reach the node when the K8s node name does not resolve.
func GetNodeSSHAddress(ctx context.Context, baseConfig *rest.Config, nodeName string) (string, error) {
	clientset, err := kubernetes.NewForConfig(baseConfig)
	if err != nil {
		return "", fmt.Errorf("create clientset: %w", err)
	}
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", nodeName, err)
	}
	var internalIP, externalIP string
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeInternalIP:
			internalIP = addr.Address
		case corev1.NodeExternalIP:
			externalIP = addr.Address
		}
	}
	if internalIP != "" {
		return internalIP, nil
	}
	if externalIP != "" {
		return externalIP, nil
	}
	return "", fmt.Errorf("node %s has no InternalIP or ExternalIP", nodeName)
}
