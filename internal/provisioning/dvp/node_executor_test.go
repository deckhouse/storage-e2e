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
	"errors"
	"testing"

	cryptossh "golang.org/x/crypto/ssh"

	sshv2 "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

const execNodeIP = "10.0.0.9"

func execTestResolver(t *testing.T) *vmIPResolver {
	t.Helper()
	virt := newFakeVirt()
	virt.seedVM("ns", "node-x", execNodeIP)
	return &vmIPResolver{virt: virt, namespace: "ns"}
}

func TestNodeExecutorExecNonZeroExitIsNotError(t *testing.T) {
	t.Parallel()
	// A completed command with a non-zero exit surfaces as ExecResult.ExitCode
	// with a nil error (SSH reports it as *ssh.ExitError).
	exec := &funcExecutor{fn: func(context.Context, string) (sshv2.ExecResult, error) {
		return sshv2.ExecResult{Stdout: []byte("out"), Stderr: []byte("err"), ExitCode: 7}, &cryptossh.ExitError{}
	}}
	conn := routeConnector{execs: map[string]*funcExecutor{execNodeIP: exec}}
	e := &dvpNodeExecutor{connector: conn, resolver: execTestResolver(t)}

	res, err := e.Exec(context.Background(), "node-x", "false")
	if err != nil {
		t.Fatalf("Exec() error = %v, want nil for clean non-zero exit", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
	if string(res.Stdout) != "out" || string(res.Stderr) != "err" {
		t.Errorf("stdout/stderr = %q/%q, want out/err", res.Stdout, res.Stderr)
	}
}

func TestNodeExecutorExecTransportErrorIsWrapped(t *testing.T) {
	t.Parallel()
	transport := errors.New("session dropped")
	exec := &funcExecutor{fn: func(context.Context, string) (sshv2.ExecResult, error) {
		return sshv2.ExecResult{}, transport
	}}
	conn := routeConnector{execs: map[string]*funcExecutor{execNodeIP: exec}}
	e := &dvpNodeExecutor{connector: conn, resolver: execTestResolver(t)}

	_, err := e.Exec(context.Background(), "node-x", "whoami")
	if !errors.Is(err, transport) {
		t.Errorf("Exec() error = %v, want wrap of %v", err, transport)
	}
}

func TestNodeExecutorExecResolverError(t *testing.T) {
	t.Parallel()
	virt := newFakeVirt() // no VM seeded -> Get returns NotFound
	e := &dvpNodeExecutor{
		connector: routeConnector{execs: map[string]*funcExecutor{}},
		resolver:  &vmIPResolver{virt: virt, namespace: "ns"},
	}

	if _, err := e.Exec(context.Background(), "missing", "id"); err == nil {
		t.Fatal("Exec() error = nil, want resolve failure")
	}
}

func TestNodeExecutorExecConnectError(t *testing.T) {
	t.Parallel()
	// routeConnector.VMExecutor returns an error for an unmapped IP.
	e := &dvpNodeExecutor{
		connector: routeConnector{execs: map[string]*funcExecutor{}},
		resolver:  execTestResolver(t),
	}

	if _, err := e.Exec(context.Background(), "node-x", "id"); err == nil {
		t.Fatal("Exec() error = nil, want VMExecutor connect failure")
	}
}
