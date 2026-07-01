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
	"time"
)

const (
	dockerReadyPoll    = 10 * time.Second
	dockerReadyTimeout = 5 * time.Minute
)

func buildDockerReadyCommand() string {
	return "sudo docker info >/dev/null 2>&1"
}

func waitDockerReady(ctx context.Context, exec remoteExecutor, poll, timeout time.Duration) error {
	cmd := buildDockerReadyCommand()

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		res, err := exec.Exec(waitCtx, cmd)
		if err == nil && res.ExitCode == 0 {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("waiting for docker to become ready within %s: %w", timeout, waitCtx.Err())
		case <-ticker.C:
		}
	}
}
