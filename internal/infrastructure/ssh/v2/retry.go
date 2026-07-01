/*
Copyright 2026 Flant JSC

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
	"context"
	"errors"
	"fmt"
	"time"
)

func NewWithRetry(ctx context.Context, d Dialer, retryEvery, timeout time.Duration, opts ...Option) (*Client, error) {
	if d == nil {
		return nil, errors.New("ssh: nil dialer")
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		c, err := New(ctx, d, opts...)
		if err == nil {
			return c, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("ssh: connect to %s within %s: %w", d.Describe(), timeout, err)
		case <-time.After(retryEvery):
		}
	}
}
