/*
Copyright 2025 Flant JSC

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
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"invalid", slog.LevelInfo}, // Default to info
		{"", slog.LevelInfo},        // Default to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseLevel(tt.input)
			if result != tt.expected {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConsoleHandler(t *testing.T) {
	tests := []struct {
		name        string
		level       slog.Level
		useColors   bool
		logFunc     func(*slog.Logger)
		contains    []string
		notContains []string
	}{
		{
			name:      "info level",
			level:     slog.LevelInfo,
			useColors: false,
			logFunc: func(l *slog.Logger) {
				l.Info("test message")
			},
			contains: []string{"[INFO]", "test message"},
		},
		{
			name:      "debug level with debug message",
			level:     slog.LevelDebug,
			useColors: false,
			logFunc: func(l *slog.Logger) {
				l.Debug("debug message")
			},
			contains: []string{"[DEBUG]", "debug message"},
		},
		{
			name:      "info level filters out debug",
			level:     slog.LevelInfo,
			useColors: false,
			logFunc: func(l *slog.Logger) {
				l.Debug("debug message")
				l.Info("info message")
			},
			contains:    []string{"[INFO]", "info message"},
			notContains: []string{"[DEBUG]", "debug message"},
		},
		{
			name:      "message with emoji in content",
			level:     slog.LevelInfo,
			useColors: false,
			logFunc: func(l *slog.Logger) {
				l.Info("✅ test message")
			},
			contains: []string{"✅", "[INFO]", "test message"},
		},
		{
			name:      "with attributes",
			level:     slog.LevelInfo,
			useColors: false,
			logFunc: func(l *slog.Logger) {
				l.Info("test message", "key", "value", "num", 42)
			},
			contains: []string{"[INFO]", "test message", "key=value", "num=42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			handler := NewConsoleHandler(&buf, &ConsoleHandlerOptions{
				Level:      tt.level,
				UseColors:  tt.useColors,
				TimeFormat: "",
			})
			logger := slog.New(handler)

			tt.logFunc(logger)

			output := buf.String()

			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Errorf("Output does not contain %q\nOutput: %s", expected, output)
				}
			}

			for _, notExpected := range tt.notContains {
				if strings.Contains(output, notExpected) {
					t.Errorf("Output contains %q but shouldn't\nOutput: %s", notExpected, output)
				}
			}
		})
	}
}

func TestMultiHandler(t *testing.T) {
	var buf1 bytes.Buffer
	var buf2 bytes.Buffer

	handler1 := NewConsoleHandler(&buf1, &ConsoleHandlerOptions{
		Level:     slog.LevelInfo,
		UseColors: false,
	})
	handler2 := NewConsoleHandler(&buf2, &ConsoleHandlerOptions{
		Level:     slog.LevelInfo,
		UseColors: false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	logger := slog.New(multiHandler)

	logger.Info("✅ test message")

	output1 := buf1.String()
	output2 := buf2.String()

	// Both handlers should have received the message
	if !strings.Contains(output1, "test message") {
		t.Errorf("Handler 1 did not receive the message\nOutput: %s", output1)
	}
	if !strings.Contains(output2, "test message") {
		t.Errorf("Handler 2 did not receive the message\nOutput: %s", output2)
	}

	// Both should have emoji since it's part of the message
	if !strings.Contains(output1, "✅") {
		t.Errorf("Handler 1 did not show emoji\nOutput: %s", output1)
	}
	if !strings.Contains(output2, "✅") {
		t.Errorf("Handler 2 did not show emoji\nOutput: %s", output2)
	}
}

func TestHelperFunctions(t *testing.T) {
	var buf bytes.Buffer
	testLogger := NewTestLogger(&buf, slog.LevelDebug)
	SetLogger(testLogger)

	tests := []struct {
		name     string
		logFunc  func()
		contains []string
	}{
		{
			name: "Step",
			logFunc: func() {
				Step(1, "Loading configuration from %s", "test.yml")
			},
			contains: []string{"Step 1", "Loading configuration from test.yml"},
		},
		{
			name: "Success",
			logFunc: func() {
				Success("Operation completed")
			},
			contains: []string{"Operation completed"},
		},
		{
			name: "Error",
			logFunc: func() {
				Error("Failed to connect to %s", "host")
			},
			contains: []string{"Failed to connect to host"},
		},
		{
			name: "Debug",
			logFunc: func() {
				Debug("Debug info: %d", 42)
			},
			contains: []string{"Debug info: 42"},
		},
		{
			name: "Warn",
			logFunc: func() {
				Warn("Resource already exists")
			},
			contains: []string{"Resource already exists"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			tt.logFunc()
			output := buf.String()

			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Errorf("Output does not contain %q\nOutput: %s", expected, output)
				}
			}
		})
	}
}

func TestWithFields(t *testing.T) {
	var buf bytes.Buffer
	testLogger := NewTestLogger(&buf, slog.LevelInfo)
	SetLogger(testLogger)

	contextLogger := WithFields(map[string]interface{}{
		"vm":        "test-vm",
		"namespace": "default",
	})

	contextLogger.Info("VM created")

	output := buf.String()
	if !strings.Contains(output, "vm=test-vm") {
		t.Errorf("Output does not contain vm field\nOutput: %s", output)
	}
	if !strings.Contains(output, "namespace=default") {
		t.Errorf("Output does not contain namespace field\nOutput: %s", output)
	}
}
