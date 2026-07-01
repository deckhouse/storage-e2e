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

package dvp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	ssh "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const maxConcurrentJoinOps = 8

var (
	joinRetryCount        = config.SSHRetryCount
	joinRetryInitialDelay = config.SSHRetryInitialDelay
	joinRetryMaxDelay     = config.SSHRetryMaxDelay
)

var joinRetryMarkers = []string{
	"HTTP Error 401",
	"Unauthorized",
	"Connection refused",
}

func buildNodeBootstrapCommand(script string) string {
	return fmt.Sprintf("sudo bash <<'BOOTSTRAP_EOF'\n%s\nBOOTSTRAP_EOF", script)
}

func isRetryableJoinError(res ssh.ExecResult, err error) bool {
	if err == nil {
		return false
	}
	combined := string(res.Stdout) + "\n" + string(res.Stderr)
	for _, marker := range joinRetryMarkers {
		if strings.Contains(combined, marker) {
			return true
		}
	}
	return false
}

func (p *dvpProvider) joinNodes(ctx context.Context, target *rest.Config, def *config.ClusterDefinition) error {
	if err := kubernetes.CreateStaticNodeGroup(ctx, target, workerNodeGroupName); err != nil {
		return fmt.Errorf("create worker nodegroup: %w", err)
	}

	cs, err := kubernetes.NewClientsetWithRetry(ctx, target)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	return p.joinNodesWithClient(ctx, cs, def)
}

// nodeJoin pairs a node with the bootstrap script it should run.
type nodeJoin struct {
	node   config.ClusterNode
	script string
}

func (p *dvpProvider) joinNodesWithClient(ctx context.Context, cs k8s.Interface, def *config.ClusterDefinition) error {
	extraMasters := def.Masters
	if len(extraMasters) > 0 {
		extraMasters = extraMasters[1:]
	}

	if len(extraMasters) == 0 && len(def.Workers) == 0 {
		return nil
	}

	var joins []nodeJoin
	if len(def.Workers) > 0 {
		workerScript, err := getBootstrapScript(ctx, cs, workerBootstrapSecret)
		if err != nil {
			return err
		}
		for _, w := range def.Workers {
			joins = append(joins, nodeJoin{node: w, script: workerScript})
		}
	}
	if len(extraMasters) > 0 {
		masterScript, err := getBootstrapScript(ctx, cs, masterBootstrapSecret)
		if err != nil {
			return err
		}
		for _, m := range extraMasters {
			joins = append(joins, nodeJoin{node: m, script: masterScript})
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentJoinOps)
	for _, j := range joins {
		g.Go(func() error {
			return p.joinNode(gctx, j.node, j.script)
		})
	}
	return g.Wait()
}

// joinNode runs the bootstrap script on a single node, retrying transient
// cluster-side failures with exponential backoff bounded by config.SSHRetryCount.
func (p *dvpProvider) joinNode(ctx context.Context, node config.ClusterNode, script string) error {
	if node.IPAddress == "" {
		return fmt.Errorf("join node %s: IP address is not set", node.Hostname)
	}

	exec, closeExec, err := p.deps.connector.VMExecutor(ctx, node.IPAddress)
	if err != nil {
		return fmt.Errorf("join node %s: connect: %w", node.Hostname, err)
	}
	defer closeExec()

	cmd := buildNodeBootstrapCommand(script)
	var lastErr error
	for attempt := 1; attempt <= joinRetryCount; attempt++ {
		res, execErr := exec.Exec(ctx, cmd)
		if execErr == nil && res.ExitCode == 0 {
			return nil
		}
		lastErr = execErr
		if lastErr == nil {
			lastErr = fmt.Errorf("bootstrap script exited with code %d", res.ExitCode)
		}

		if !isRetryableJoinError(res, execErr) || attempt == joinRetryCount {
			p.logger.Warn("node join failed",
				"node", node.Hostname, "attempt", attempt,
				"exitCode", res.ExitCode,
				"stdout", string(res.Stdout), "stderr", string(res.Stderr))
			return fmt.Errorf("join node %s after %d attempt(s): %w", node.Hostname, attempt, lastErr)
		}

		backoff := joinRetryInitialDelay * time.Duration(1<<uint(attempt-1))
		if backoff > joinRetryMaxDelay {
			backoff = joinRetryMaxDelay
		}
		p.logger.Warn("node join failed, retrying",
			"node", node.Hostname, "attempt", attempt, "backoff", backoff, "err", lastErr)

		select {
		case <-ctx.Done():
			return fmt.Errorf("join node %s: %w", node.Hostname, ctx.Err())
		case <-time.After(backoff):
		}
	}
	return fmt.Errorf("join node %s after %d attempt(s): %w", node.Hostname, joinRetryCount, lastErr)
}

// getBootstrapScript reads the decoded bootstrap.sh out of a bootstrap secret.
func getBootstrapScript(ctx context.Context, cs k8s.Interface, secretName string) (string, error) {
	secret, err := cs.CoreV1().Secrets(bootstrapSecretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("bootstrap secret %s/%s not found", bootstrapSecretNamespace, secretName)
		}
		return "", fmt.Errorf("get bootstrap secret %s/%s: %w", bootstrapSecretNamespace, secretName, err)
	}
	script, ok := secret.Data[bootstrapScriptKey]
	if !ok || len(script) == 0 {
		return "", fmt.Errorf("bootstrap secret %s/%s has no %q key", bootstrapSecretNamespace, secretName, bootstrapScriptKey)
	}
	return string(script), nil
}
