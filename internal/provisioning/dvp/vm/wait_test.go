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

package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitForConditionDoneImmediately(t *testing.T) {
	calls := 0
	get := func(context.Context) (int, error) { calls++; return 0, nil }
	cond := func(int, error) (bool, error) { return true, nil }

	if err := waitForCondition(context.Background(), time.Millisecond, get, cond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("get called %d times, want 1 (no wait before first check)", calls)
	}
}

func TestWaitForConditionDoneAfterPolling(t *testing.T) {
	calls := 0
	get := func(context.Context) (int, error) { calls++; return calls, nil }
	cond := func(v int, _ error) (bool, error) { return v >= 3, nil }

	if err := waitForCondition(context.Background(), time.Millisecond, get, cond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("get called %d times, want 3", calls)
	}
}

func TestWaitForConditionReturnsCondError(t *testing.T) {
	sentinel := errors.New("boom")
	get := func(context.Context) (int, error) { return 0, nil }
	cond := func(int, error) (bool, error) { return false, sentinel }

	err := waitForCondition(context.Background(), time.Millisecond, get, cond)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

func TestWaitForConditionContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	get := func(context.Context) (int, error) { return 0, nil }
	cond := func(int, error) (bool, error) { return false, nil }

	err := waitForCondition(ctx, time.Millisecond, get, cond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWaitForConditionTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	get := func(context.Context) (int, error) { return 0, nil }
	cond := func(int, error) (bool, error) { return false, nil }

	err := waitForCondition(ctx, time.Millisecond, get, cond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestWaitForConditionTransientGetErrorsThenSuccess(t *testing.T) {
	transient := errors.New("transient")
	calls := 0
	get := func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, transient
		}
		return calls, nil
	}
	// cond mimics the readiness funcs: transient getErr keeps polling.
	cond := func(_ int, getErr error) (bool, error) {
		if getErr != nil {
			return false, nil
		}
		return true, nil
	}

	if err := waitForCondition(context.Background(), time.Millisecond, get, cond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("get called %d times, want 3 (kept polling through transient errors)", calls)
	}
}

func TestWaitForConditionTimeoutWrapsLastError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	underlying := errors.New("api unavailable")
	get := func(context.Context) (int, error) { return 0, underlying }
	cond := func(_ int, getErr error) (bool, error) {
		if getErr != nil {
			return false, nil
		}
		return false, nil
	}

	err := waitForCondition(ctx, time.Millisecond, get, cond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "api unavailable") {
		t.Errorf("err = %v, want it to mention the last underlying error", err)
	}
}
