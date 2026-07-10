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

package commander

import (
	"context"
	"strings"
	"testing"
)

func TestSetMasterCount_RejectsInvalidCount(t *testing.T) {
	// The count guard runs before any config parsing or network access, so these
	// fail fast regardless of the environment.
	for _, n := range []int{0, 2, 4, -1} {
		if err := SetMasterCount(context.Background(), n); err == nil {
			t.Errorf("SetMasterCount(%d) = nil, want a validation error", n)
		} else if !strings.Contains(err.Error(), "must be 1 or 3") {
			t.Errorf("SetMasterCount(%d) error = %q, want it to mention the 1|3 constraint", n, err.Error())
		}
	}
}
