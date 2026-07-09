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
)

func TestVMIPResolverResolve(t *testing.T) {
	t.Parallel()

	t.Run("returns IP when set", func(t *testing.T) {
		t.Parallel()
		virt := newFakeVirt()
		virt.seedVM("ns", "node-1", "10.0.0.5")
		r := &vmIPResolver{virt: virt, namespace: "ns"}

		got, err := r.Resolve(context.Background(), "node-1")
		if err != nil {
			t.Fatalf("Resolve() error = %v, want nil", err)
		}
		if got != "10.0.0.5" {
			t.Errorf("Resolve() = %q, want 10.0.0.5", got)
		}
	})

	t.Run("empty IP is an error", func(t *testing.T) {
		t.Parallel()
		virt := newFakeVirt()
		virt.seedVM("ns", "node-1", "")
		r := &vmIPResolver{virt: virt, namespace: "ns"}

		if _, err := r.Resolve(context.Background(), "node-1"); err == nil {
			t.Fatal("Resolve() error = nil, want no-IP error")
		}
	})

	t.Run("get error is wrapped", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("api down")
		virt := newFakeVirt()
		virt.getVMErr = sentinel
		r := &vmIPResolver{virt: virt, namespace: "ns"}

		_, err := r.Resolve(context.Background(), "node-1")
		if !errors.Is(err, sentinel) {
			t.Errorf("Resolve() error = %v, want wrap of %v", err, sentinel)
		}
	})
}
