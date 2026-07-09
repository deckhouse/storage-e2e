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

// Package conformance verifies that a cluster provider honors the pkg/e2e
// capability contracts (NodeExecutor) against a live cluster. Every provider
// must pass it — that is what keeps the semantics of the modes (dvp, commander,
// future ones) from silently diverging.
//
// It runs against real infrastructure, so it is invoked explicitly, e.g. from
// a dedicated suite or a plain test:
//
//	cl, _ := e2e.Connect(ctx)
//	defer cl.Close(ctx)
//	report := conformance.Verify(ctx, cl, conformance.Config{NodeName: "worker-0"})
//	if err := report.Err(); err != nil { ... }
package conformance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/storage-e2e/pkg/e2e"
)

// Config parametrizes a conformance run.
type Config struct {
	// NodeName is the node the checks run against. When empty, the first
	// worker (non-control-plane) node of the cluster is used.
	NodeName string
}

// Result is the outcome of one conformance check.
type Result struct {
	Name string
	Err  error
}

// Report aggregates the results of a conformance run.
type Report struct {
	Results []Result
}

// Err joins the errors of all failed checks, or returns nil when every check
// passed.
func (r *Report) Err() error {
	var errs []error
	for _, res := range r.Results {
		if res.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", res.Name, res.Err))
		}
	}
	return errors.Join(errs...)
}

// Verify runs all conformance checks against the connected cluster and
// returns a per-check report. It does not stop on the first failure.
func Verify(ctx context.Context, cluster *e2e.Cluster, cfg Config) *Report {
	report := &Report{}
	record := func(name string, err error) {
		report.Results = append(report.Results, Result{Name: name, Err: err})
	}

	nodeName := cfg.NodeName
	if nodeName == "" {
		resolved, err := pickWorkerNode(ctx, cluster)
		if err != nil {
			record("resolve target node", err)
			return report
		}
		nodeName = resolved
	}

	record("node executor", VerifyNodeExecutor(ctx, cluster.Nodes(), nodeName))
	return report
}

// VerifyNodeExecutor checks the NodeExecutor contract on the given node:
// stdout/stderr are captured separately, the exit code of a completed command
// is reported without an error, and sudo is available non-interactively.
func VerifyNodeExecutor(ctx context.Context, nodes e2e.NodeExecutor, nodeName string) error {
	res, err := nodes.Exec(ctx, nodeName, "echo -n conformance-stdout; echo -n conformance-stderr 1>&2")
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if got := string(res.Stdout); got != "conformance-stdout" {
		return fmt.Errorf("stdout mismatch: %q", got)
	}
	if got := string(res.Stderr); got != "conformance-stderr" {
		return fmt.Errorf("stderr mismatch: %q", got)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("unexpected exit code %d", res.ExitCode)
	}

	res, err = nodes.Exec(ctx, nodeName, "exit 42")
	if err != nil {
		return fmt.Errorf("non-zero exit must not be an error, got: %w", err)
	}
	if res.ExitCode != 42 {
		return fmt.Errorf("exit code not propagated: got %d, want 42", res.ExitCode)
	}

	res, err = nodes.Exec(ctx, nodeName, "sudo -n true")
	if err != nil {
		return fmt.Errorf("sudo check: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("passwordless sudo unavailable (exit %d): %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

func pickWorkerNode(ctx context.Context, cluster *e2e.Cluster) (string, error) {
	cs, err := cluster.Clientset()
	if err != nil {
		return "", err
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			continue
		}
		if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
			continue
		}
		return node.Name, nil
	}
	return "", fmt.Errorf("no worker nodes found")
}
