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

package logger

import (
	"log/slog"
	"testing"
)

func TestLevelToString(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARN"},
		{slog.LevelError, "ERROR"},
		// Unknown/out-of-range level returns "INFO".
		{slog.Level(100), "INFO"},
		{slog.Level(-100), "INFO"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := LevelToString(tc.level)
			if got != tc.want {
				t.Errorf("LevelToString(%v)=%q, want %q", tc.level, got, tc.want)
			}
		})
	}
}

// TestParseLevelLevelToStringRoundTrip checks that the canonical pairs survive
// a string -> Level -> string trip. Note: ParseLevel accepts "warning" as a
// synonym for "warn" but LevelToString always emits "WARN".
func TestParseLevelLevelToStringRoundTrip(t *testing.T) {
	pairs := map[string]string{
		"debug": "DEBUG",
		"info":  "INFO",
		"warn":  "WARN",
		"error": "ERROR",
	}
	for in, want := range pairs {
		t.Run(in, func(t *testing.T) {
			got := LevelToString(ParseLevel(in))
			if got != want {
				t.Errorf("LevelToString(ParseLevel(%q))=%q, want %q", in, got, want)
			}
		})
	}

	// "warning" is a synonym for warn.
	if got := LevelToString(ParseLevel("warning")); got != "WARN" {
		t.Errorf("ParseLevel(\"warning\") roundtrip = %q, want WARN", got)
	}
}
