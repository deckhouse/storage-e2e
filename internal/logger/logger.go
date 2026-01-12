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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/deckhouse/storage-e2e/internal/config"
)

var (
	defaultLogger *slog.Logger
	logFile       *os.File
	logFileMutex  sync.Mutex
)

// Initialize sets up the global logger based on configuration.
// It creates handlers for both console (with emojis) and file (JSON) output.
// File logging is enabled if config.LogFilePath is set and not empty.
func Initialize() error {
	level := ParseLevel(config.LogLevel)

	handlers := make([]slog.Handler, 0, 2)

	// Always add console handler with emojis for stdout
	consoleHandler := NewConsoleHandler(os.Stdout, &ConsoleHandlerOptions{
		Level:      level,
		UseEmojis:  true,
		UseColors:  true,
		TimeFormat: "", // No timestamp for console (cleaner output)
	})
	handlers = append(handlers, consoleHandler)

	// Add file handler if LogFilePath is specified
	if config.LogFilePath != "" {
		// Create log directory if it doesn't exist
		logDir := filepath.Dir(config.LogFilePath)
		if logDir != "" && logDir != "." {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
			}
		}

		var err error
		logFile, err = os.OpenFile(config.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %w", config.LogFilePath, err)
		}

		// File handler uses JSON format for machine parsing
		fileHandler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				// Customize timestamp format in JSON logs
				if a.Key == slog.TimeKey {
					return slog.String(slog.TimeKey, a.Value.Time().Format(time.RFC3339))
				}
				return a
			},
		})
		handlers = append(handlers, fileHandler)
	}

	// Create multi-handler to write to all handlers
	multiHandler := NewMultiHandler(handlers...)
	defaultLogger = slog.New(multiHandler)

	return nil
}

// Close closes the log file if it was opened
func Close() error {
	logFileMutex.Lock()
	defer logFileMutex.Unlock()

	if logFile != nil {
		if err := logFile.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}
		logFile = nil
	}
	return nil
}

// GetLogger returns the default logger instance
func GetLogger() *slog.Logger {
	if defaultLogger == nil {
		// Fallback to default slog logger if Initialize wasn't called
		return slog.Default()
	}
	return defaultLogger
}

// SetLogger sets a custom logger (useful for testing)
func SetLogger(logger *slog.Logger) {
	defaultLogger = logger
}

// Helper functions that provide a clean API matching the current fmt.Printf style

// Step logs a major step in the workflow with a step number
func Step(step int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(fmt.Sprintf("Step %d: %s", step, msg), "emoji", "▶️", "type", "step")
}

// StepComplete logs the completion of a step
func StepComplete(step int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(fmt.Sprintf("Step %d: %s", step, msg), "emoji", "✅", "type", "step_complete")
}

// Success logs a successful operation
func Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(msg, "emoji", "✅", "type", "success")
}

// Info logs general informational messages
func Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(msg)
}

// Warn logs warning messages
func Warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Warn(msg, "emoji", "⚠️", "type", "warning")
}

// Error logs error messages
func Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Error(msg, "emoji", "❌", "type", "error")
}

// Debug logs detailed debugging information
func Debug(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Debug(msg, "emoji", "🐛", "type", "debug")
}

// Progress logs progress indicators (like waiting, polling)
func Progress(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(msg, "emoji", "⏳", "type", "progress")
}

// Skip logs skipped operations
func Skip(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(msg, "emoji", "⏭️", "type", "skip")
}

// Delete logs deletion operations
func Delete(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	GetLogger().Info(msg, "emoji", "🗑️", "type", "delete")
}

// WithField returns a logger with an additional field
func WithField(key string, value interface{}) *slog.Logger {
	return GetLogger().With(key, value)
}

// WithFields returns a logger with multiple additional fields
func WithFields(fields map[string]interface{}) *slog.Logger {
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return GetLogger().With(args...)
}

// NewTestLogger creates a logger for testing that only writes to the provided writer
func NewTestLogger(w io.Writer, level slog.Level) *slog.Logger {
	handler := NewConsoleHandler(w, &ConsoleHandlerOptions{
		Level:      level,
		UseEmojis:  false, // Disable emojis in tests for cleaner output
		UseColors:  false, // Disable colors in tests
		TimeFormat: "",
	})
	return slog.New(handler)
}
