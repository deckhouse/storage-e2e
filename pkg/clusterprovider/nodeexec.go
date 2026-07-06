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

package clusterprovider

import "context"

// ExecResult is the outcome of a command executed on a cluster node.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// NodeExecutor runs commands on cluster nodes. Implementations resolve the
// node name to a reachable address themselves (e.g. a nested VM IP behind a
// jump host for DVP, or the node InternalIP behind the master for Commander).
//
// Contract: a command that ran to completion with a non-zero exit code is NOT
// an error — the exit code is reported in ExecResult.ExitCode and err is nil.
// err is reserved for transport/infrastructure failures (connection, session,
// context cancellation).
type NodeExecutor interface {
	Exec(ctx context.Context, nodeName, command string) (ExecResult, error)
}
