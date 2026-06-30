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
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// cviNameMaxLen is the DNS-1123 label limit Kubernetes enforces on object names.
	cviNameMaxLen = 63
	// cviHashLen is how many hex chars of the URL hash we append for uniqueness.
	cviHashLen = 8
	// cviBaseMaxLen reserves room for the "-<hash8>" suffix within the 63-char limit.
	cviBaseMaxLen = cviNameMaxLen - cviHashLen - 1
)

// cviNameFromImageURL builds a deterministic, collision-resistant, DNS-1123-safe
// CVI name from an image URL: a sanitized basename plus the first cviHashLen hex
// chars of sha256(imageURL). Two URLs with the same basename therefore yield
// different names, and the result is always a valid label of length <= 63.
func cviNameFromImageURL(imageURL string) string {
	base := sanitizeCVIBase(imageURL)

	sum := sha256.Sum256([]byte(imageURL))
	hash8 := hex.EncodeToString(sum[:])[:cviHashLen]

	return base + "-" + hash8
}

// sanitizeCVIBase reduces the image URL's basename to a DNS-1123-safe fragment
// of at most cviBaseMaxLen chars, falling back to "image" when nothing usable
// remains. The trailing hash suffix (hex) guarantees a valid alphanumeric end.
func sanitizeCVIBase(imageURL string) string {
	parts := strings.Split(imageURL, "/")
	name := parts[len(parts)-1]

	for _, ext := range []string{".img", ".qcow2", ".raw", ".iso", ".vmdk", ".vdi", ".gz", ".xz"} {
		name = strings.TrimSuffix(name, ext)
	}

	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, name)
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if name == "" {
		return "image"
	}

	if len(name) > cviBaseMaxLen {
		name = strings.TrimRight(name[:cviBaseMaxLen], "-")
		if name == "" {
			return "image"
		}
	}
	return name
}

// systemDiskName returns the deterministic VirtualDisk name for a VM's system
// disk. vmName is already a DNS-safe hostname, so no sanitization is needed here.
func systemDiskName(vmName string) string {
	return vmName + "-system"
}
