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

import "testing"

func TestCVINameFromImageURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img", "jammy-server-cloudimg-amd64"},
		{"https://example/redos-8-1.x86_64.qcow2", "redos-8-1-x86-64"},
		{"https://example/debian_12.qcow2", "debian-12"},
		{"https://example/image..name--with...junk.img", "image-name-with-junk"},
		{"https://example/-weird-.img", "weird"},
		{"https://example/.img", "image"},
	}
	for _, tt := range tests {
		if got := cviNameFromImageURL(tt.url); got != tt.want {
			t.Errorf("cviNameFromImageURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestSystemDiskName(t *testing.T) {
	if got := systemDiskName("master-1"); got != "master-1-system" {
		t.Errorf("systemDiskName = %q, want master-1-system", got)
	}
}
