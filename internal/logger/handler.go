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
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// ConsoleHandlerOptions defines options for the console handler
type ConsoleHandlerOptions struct {
	Level      slog.Level
	UseColors  bool
	TimeFormat string // Empty string means no timestamp
}

// ConsoleHandler is a custom slog.Handler that formats logs for human-readable console output
type ConsoleHandler struct {
	opts   ConsoleHandlerOptions
	writer io.Writer
	mu     sync.Mutex
	attrs  []slog.Attr
	groups []string
}

// NewConsoleHandler creates a new console handler
func NewConsoleHandler(w io.Writer, opts *ConsoleHandlerOptions) *ConsoleHandler {
	if opts == nil {
		opts = &ConsoleHandlerOptions{
			Level:      slog.LevelInfo,
			UseColors:  true,
			TimeFormat: "",
		}
	}
	return &ConsoleHandler{
		opts:   *opts,
		writer: w,
		attrs:  make([]slog.Attr, 0),
		groups: make([]string, 0),
	}
}

// Enabled reports whether the handler handles records at the given level
func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level
}

// Handle handles the Record
func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var buf []byte

	// Add level indicator with optional color
	if h.opts.UseColors {
		buf = append(buf, h.colorizeLevel(r.Level)...)
	} else {
		buf = append(buf, fmt.Sprintf("[%s] ", LevelToString(r.Level))...)
	}

	// Add message (emojis are now part of the message itself)
	buf = append(buf, r.Message...)

	// Add attributes (excluding type which is metadata)
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "type" {
			attrs = append(attrs, a)
		}
		return true
	})

	// Also add handler's stored attributes
	attrs = append(attrs, h.attrs...)

	if len(attrs) > 0 {
		buf = append(buf, " ["...)
		for i, attr := range attrs {
			if i > 0 {
				buf = append(buf, ", "...)
			}
			buf = append(buf, fmt.Sprintf("%s=%v", attr.Key, attr.Value)...)
		}
		buf = append(buf, "]"...)
	}

	// Add newline
	buf = append(buf, '\n')

	// Write to output
	_, err := h.writer.Write(buf)
	return err
}

// WithAttrs returns a new Handler whose attributes consist of h's attributes followed by attrs
func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)

	return &ConsoleHandler{
		opts:   h.opts,
		writer: h.writer,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

// WithGroup returns a new Handler with the given group appended to the receiver's existing groups
func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name

	return &ConsoleHandler{
		opts:   h.opts,
		writer: h.writer,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

// colorizeLevel adds ANSI color codes to the level string
func (h *ConsoleHandler) colorizeLevel(level slog.Level) string {
	const (
		colorReset  = "\033[0m"
		colorRed    = "\033[31m"
		colorYellow = "\033[33m"
		colorBlue   = "\033[34m"
		colorGray   = "\033[90m"
	)

	switch level {
	case slog.LevelDebug:
		return colorGray + "[DEBUG]" + colorReset + " "
	case slog.LevelInfo:
		return colorBlue + "[INFO]" + colorReset + "  "
	case slog.LevelWarn:
		return colorYellow + "[WARN]" + colorReset + "  "
	case slog.LevelError:
		return colorRed + "[ERROR]" + colorReset + " "
	default:
		return "[INFO]  "
	}
}
