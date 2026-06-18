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

package ssh

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestRouteHopsAndDescribe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		first    Endpoint
		more     []Endpoint
		wantHops int
		wantDesc string
	}{
		{
			name:     "direct",
			first:    Endpoint{User: "root", Addr: "target"},
			wantHops: 1,
			wantDesc: "root@target:22",
		},
		{
			name:     "single jump",
			first:    Endpoint{User: "bastion", Addr: "jump:2222"},
			more:     []Endpoint{{User: "root", Addr: "target"}},
			wantHops: 2,
			wantDesc: "bastion@jump:2222 -> root@target:22",
		},
		{
			name:  "two jumps preserve order",
			first: Endpoint{User: "a", Addr: "h1"},
			more: []Endpoint{
				{User: "b", Addr: "h2"},
				{User: "c", Addr: "h3"},
			},
			wantHops: 3,
			wantDesc: "a@h1:22 -> b@h2:22 -> c@h3:22",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := Route(tc.first, tc.more...)
			r, ok := d.(*route)
			if !ok {
				t.Fatalf("Route returned %T, want *route", d)
			}
			if len(r.hops) != tc.wantHops {
				t.Fatalf("hops = %d, want %d", len(r.hops), tc.wantHops)
			}
			if got := d.Describe(); got != tc.wantDesc {
				t.Fatalf("Describe() = %q, want %q", got, tc.wantDesc)
			}
		})
	}
}

// recordCloser records the order in which it is closed and can fail on demand.
type recordCloser struct {
	id    int
	order *[]int
	mu    *sync.Mutex
	err   error
}

func (c recordCloser) Close() error {
	c.mu.Lock()
	*c.order = append(*c.order, c.id)
	c.mu.Unlock()
	return c.err
}

func TestChainCloserReverseOrderAndNilSkip(t *testing.T) {
	t.Parallel()

	var order []int
	var mu sync.Mutex
	cc := &chainCloser{}

	cc.add(recordCloser{id: 1, order: &order, mu: &mu})
	cc.add(nil) // must be skipped without panicking
	cc.add(recordCloser{id: 2, order: &order, mu: &mu})
	cc.add(recordCloser{id: 3, order: &order, mu: &mu})

	if err := cc.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	want := []int{3, 2, 1}
	if len(order) != len(want) {
		t.Fatalf("close order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("close order = %v, want %v", order, want)
		}
	}
}

func TestChainCloserAggregatesErrors(t *testing.T) {
	t.Parallel()

	var order []int
	var mu sync.Mutex
	boom := errors.New("close boom")
	cc := &chainCloser{}
	cc.add(recordCloser{id: 1, order: &order, mu: &mu, err: boom})
	cc.add(recordCloser{id: 2, order: &order, mu: &mu})

	err := cc.Close()
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("Close() = %v, want error wrapping %v", err, boom)
	}
}

// transientCloser returns a transient error from Close; chainCloser must ignore
// it (an already-dead peer is not a close failure worth surfacing).
type transientCloser struct{}

func (transientCloser) Close() error { return fmt.Errorf("read: %w", io.EOF) }

func TestChainCloserIgnoresTransientCloseErrors(t *testing.T) {
	t.Parallel()

	cc := &chainCloser{}
	cc.add(transientCloser{})
	if err := cc.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil (transient close errors ignored)", err)
	}
}
