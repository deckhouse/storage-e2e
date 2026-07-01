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
	"time"

	sshv2 "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
)

func TestBuildDockerReadyCommand(t *testing.T) {
	t.Parallel()

	const want = "sudo docker info >/dev/null 2>&1"
	if got := buildDockerReadyCommand(); got != want {
		t.Errorf("buildDockerReadyCommand() = %q, want %q", got, want)
	}
}

type scriptedExecutor struct {
	results []execStep
	calls   int
}

type execStep struct {
	res sshv2.ExecResult
	err error
}

func (e *scriptedExecutor) Exec(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return sshv2.ExecResult{}, err
	}
	step := e.results[min(e.calls, len(e.results)-1)]
	e.calls++
	return step.res, step.err
}

func TestWaitDockerReadyReadyNow(t *testing.T) {
	t.Parallel()

	exec := &scriptedExecutor{results: []execStep{{res: sshv2.ExecResult{ExitCode: 0}}}}
	if err := waitDockerReady(context.Background(), exec, time.Millisecond, time.Second); err != nil {
		t.Fatalf("waitDockerReady() error = %v, want nil", err)
	}
	if exec.calls != 1 {
		t.Errorf("Exec called %d times, want 1", exec.calls)
	}
}

func TestWaitDockerReadyReadyAfterN(t *testing.T) {
	t.Parallel()

	exec := &scriptedExecutor{results: []execStep{
		{res: sshv2.ExecResult{ExitCode: 1}},
		{err: errors.New("connection reset")},
		{res: sshv2.ExecResult{ExitCode: 0}},
	}}
	if err := waitDockerReady(context.Background(), exec, time.Millisecond, 2*time.Second); err != nil {
		t.Fatalf("waitDockerReady() error = %v, want nil", err)
	}
	if exec.calls != 3 {
		t.Errorf("Exec called %d times, want 3", exec.calls)
	}
}

func TestWaitDockerReadyNeverTimesOut(t *testing.T) {
	t.Parallel()

	exec := &scriptedExecutor{results: []execStep{{res: sshv2.ExecResult{ExitCode: 1}}}}
	err := waitDockerReady(context.Background(), exec, time.Millisecond, 30*time.Millisecond)
	if err == nil {
		t.Fatal("waitDockerReady() error = nil, want timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waitDockerReady() error = %v, want wrap of context.DeadlineExceeded", err)
	}
}

func TestWaitDockerReadyRespectsParentContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	exec := &scriptedExecutor{results: []execStep{{res: sshv2.ExecResult{ExitCode: 1}}}}
	err := waitDockerReady(ctx, exec, time.Millisecond, time.Minute)
	if err == nil {
		t.Fatal("waitDockerReady() error = nil, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("waitDockerReady() error = %v, want wrap of context.Canceled", err)
	}
}
