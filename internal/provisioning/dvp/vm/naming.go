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

import "strings"

func cviNameFromImageURL(imageURL string) string {
	parts := strings.Split(imageURL, "/")
	name := parts[len(parts)-1]

	for _, ext := range []string{".img", ".qcow2", ".raw", ".iso", ".vmdk", ".vdi", ".gz", ".xz"} {
		name = strings.TrimSuffix(name, ext)
	}

	name = strings.ToLower(name)
	name = strings.NewReplacer("_", "-", ".", "-").Replace(name)
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if name == "" {
		return "image"
	}
	return name
}

// systemDiskName returns the deterministic VirtualDisk name for a VM's system
// disk.
func systemDiskName(vmName string) string {
	return vmName + "-system"
}
