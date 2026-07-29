/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package commander

import (
	"context"
	"errors"
	"fmt"

	cryptossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type nodeAddressResolver interface {
	Resolve(ctx context.Context, nodeName string) (string, error)
}

type internalIPResolver struct {
	clientset kubernetes.Interface
}

func (r *internalIPResolver) Resolve(ctx context.Context, nodeName string) (string, error) {
	node, err := r.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get node %s: %w", nodeName, err)
	}
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
			return addr.Address, nil
		}
	}
	return "", fmt.Errorf("node %s reports no InternalIP", nodeName)
}

type commanderNodeExecutor struct {
	conn     *connector
	resolver nodeAddressResolver
	user     string
}

var _ clusterprovider.NodeExecutor = (*commanderNodeExecutor)(nil)

func (e *commanderNodeExecutor) Exec(ctx context.Context, nodeName, command string) (clusterprovider.ExecResult, error) {
	ip, err := e.resolver.Resolve(ctx, nodeName)
	if err != nil {
		return clusterprovider.ExecResult{}, err
	}

	client, err := e.conn.buildSSHClient(ctx, ip, e.user)
	if err != nil {
		return clusterprovider.ExecResult{}, fmt.Errorf("connect to node %s (%s): %w", nodeName, ip, err)
	}
	defer func() { _ = client.Close() }()

	res, err := client.Exec(ctx, command)
	out := clusterprovider.ExecResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
	}
	// Per the NodeExecutor contract a non-zero exit is not an error: the exit code
	// carries it. Only transport failures are.
	var exitErr *cryptossh.ExitError
	if errors.As(err, &exitErr) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("exec on node %s (%s): %w", nodeName, ip, err)
	}
	return out, nil
}
