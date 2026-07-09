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
	"fmt"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

// dvpNodeExecutor runs commands on test cluster nodes over SSH: the node name
// is resolved to the nested VM IP on the base cluster, and the connection goes
// through the base cluster's SSH endpoints.
type dvpNodeExecutor struct {
	connector baseConnector
	resolver  *vmIPResolver
}

var _ clusterprovider.NodeExecutor = (*dvpNodeExecutor)(nil)

func (e *dvpNodeExecutor) Exec(ctx context.Context, nodeName, command string) (clusterprovider.ExecResult, error) {
	ip, err := e.resolver.Resolve(ctx, nodeName)
	if err != nil {
		return clusterprovider.ExecResult{}, err
	}

	exec, closeExec, err := e.connector.VMExecutor(ctx, ip)
	if err != nil {
		return clusterprovider.ExecResult{}, fmt.Errorf("connect to node %s (%s): %w", nodeName, ip, err)
	}
	defer closeExec()

	res, err := exec.Exec(ctx, command)
	out := clusterprovider.ExecResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
	}
	if _, ok := errors.AsType[*cryptossh.ExitError](err); ok {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("exec on node %s (%s): %w", nodeName, ip, err)
	}
	return out, nil
}
