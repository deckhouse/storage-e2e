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

import "fmt"

// ProviderMode identifies which cluster provider implementation to use.
type ProviderMode string

// Supported provider modes.
const (
	ModeDVP       = "dvp"
	ModeCommander = "commander"
)

// UnmarshalText parses a ProviderMode from its textual form, rejecting any
// value outside the supported set. It satisfies encoding.TextUnmarshaler so the
// mode can be populated directly from environment variables.
func (m *ProviderMode) UnmarshalText(text []byte) error {
	v := ProviderMode(text)
	switch v {
	case ModeDVP, ModeCommander:
		*m = v
		return nil
	default:
		return fmt.Errorf("invalid MODE: %q (allowed: dvp, commander)", text)
	}
}

// String returns the provider mode as a plain string.
func (m ProviderMode) String() string {
	return string(m)
}
