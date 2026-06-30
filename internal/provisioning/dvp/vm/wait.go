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
	"fmt"
	"time"
)

type conditionFunc[T any] func(obj T, getErr error) (done bool, err error)

func waitForCondition[T any](
	ctx context.Context,
	interval time.Duration,
	get func(ctx context.Context) (T, error),
	cond conditionFunc[T],
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for {
		obj, getErr := get(ctx)
		if getErr != nil {
			lastErr = getErr
		}
		done, err := cond(obj, getErr)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w (last error: %w)", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
