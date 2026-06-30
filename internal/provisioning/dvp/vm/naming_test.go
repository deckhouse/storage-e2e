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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// isHex8 reports whether s is exactly 8 lowercase hex characters.
func isHex8(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// splitCVIName splits a generated CVI name into its sanitized base and the
// 8-char hash suffix, splitting on the final '-'.
func splitCVIName(t *testing.T, name string) (base, hash string) {
	t.Helper()
	i := strings.LastIndex(name, "-")
	if i < 0 {
		t.Fatalf("name %q has no '-' separator", name)
	}
	return name[:i], name[i+1:]
}

func TestCVINameFromImageURLStructure(t *testing.T) {
	tests := []struct {
		url      string
		wantBase string
	}{
		{"https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img", "jammy-server-cloudimg-amd64"},
		{"https://example/redos-8-1.x86_64.qcow2", "redos-8-1-x86-64"},
		{"https://example/debian_12.qcow2", "debian-12"},
		{"https://example/jammy+nvidia.img", "jammy-nvidia"},
		{"https://example/image..name--with...junk.img", "image-name-with-junk"},
		{"https://example/-weird-.img", "weird"},
		{"https://example/.img", "image"},
	}
	for _, tt := range tests {
		got := cviNameFromImageURL(tt.url)
		if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
			t.Errorf("cviNameFromImageURL(%q) = %q is not a valid DNS-1123 label: %v", tt.url, got, errs)
		}
		if len(got) > 63 {
			t.Errorf("cviNameFromImageURL(%q) = %q length %d > 63", tt.url, got, len(got))
		}
		base, hash := splitCVIName(t, got)
		if base != tt.wantBase {
			t.Errorf("cviNameFromImageURL(%q) base = %q, want %q", tt.url, base, tt.wantBase)
		}
		if !isHex8(hash) {
			t.Errorf("cviNameFromImageURL(%q) hash suffix = %q, want 8 lowercase hex chars", tt.url, hash)
		}
	}
}

func TestCVINameFromImageURLSanitizesQueryString(t *testing.T) {
	got := cviNameFromImageURL("https://example/jammy.img?token=abc&v=1")
	if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
		t.Errorf("cviNameFromImageURL(query) = %q is not a valid DNS-1123 label: %v", got, errs)
	}
}

func TestCVINameFromImageURLDeterministic(t *testing.T) {
	url := "https://example/jammy.img"
	if a, b := cviNameFromImageURL(url), cviNameFromImageURL(url); a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}
}

func TestCVINameFromImageURLDistinctForSameBasename(t *testing.T) {
	a := cviNameFromImageURL("https://repo-a.example/images/ubuntu.img")
	b := cviNameFromImageURL("https://repo-b.example/images/ubuntu.img")
	if a == b {
		t.Fatalf("expected different names for different URLs sharing basename %q, got %q for both", "ubuntu.img", a)
	}
	if !strings.HasPrefix(a, "ubuntu-") || !strings.HasPrefix(b, "ubuntu-") {
		t.Errorf("names %q / %q should share the sanitized base prefix %q", a, b, "ubuntu-")
	}
}

func TestCVINameFromImageURLLongBasename(t *testing.T) {
	long := "https://example/" + strings.Repeat("a", 200) + ".img"
	got := cviNameFromImageURL(long)
	if len(got) > 63 {
		t.Fatalf("cviNameFromImageURL(long) = %q length %d > 63", got, len(got))
	}
	if errs := validation.IsDNS1123Label(got); len(errs) > 0 {
		t.Errorf("cviNameFromImageURL(long) = %q is not a valid DNS-1123 label: %v", got, errs)
	}
	_, hash := splitCVIName(t, got)
	if !isHex8(hash) {
		t.Errorf("cviNameFromImageURL(long) hash suffix = %q, want 8 lowercase hex chars", hash)
	}
}

func TestSystemDiskName(t *testing.T) {
	if got := systemDiskName("master-1"); got != "master-1-system" {
		t.Errorf("systemDiskName = %q, want master-1-system", got)
	}
}
