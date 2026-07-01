// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"fmt"
	"strings"
)

// ToolError represents an error from a tool execution, formatted for LLM
// consumption. The format follows the project standard:
//
//	[tool_name error: type]
//	detail
//	Hint: actionable suggestion
type ToolError struct {
	Tool     string // The name of the tool that produced the error
	Type     string // A short error category (e.g., "file_not_found", "permission_denied")
	Detail   string // Human-readable detail about what went wrong
	HintText string // Actionable suggestion for the LLM to recover
}

// Error returns the formatted error string for LLM consumption.
func (e *ToolError) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[%s error: %s]\n", e.Tool, e.Type))
	b.WriteString(e.Detail)
	if e.HintText != "" {
		b.WriteString(fmt.Sprintf("\nHint: %s", e.HintText))
	}
	return b.String()
}

// Hint returns the actionable hint for LLM injection.
func (e *ToolError) Hint() string {
	return e.HintText
}

// ConfigError wraps configuration-related errors with the relevant config key.
type ConfigError struct {
	Key string // The config key that caused the error
	Err error  // The underlying error
}

// Error returns a formatted config error string.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("config error [%s]: %v", e.Key, e.Err)
}

// Unwrap returns the underlying error for errors.Is/errors.As.
func (e *ConfigError) Unwrap() error {
	return e.Err
}

// Hint returns a generic configuration hint.
func (e *ConfigError) Hint() string {
	return fmt.Sprintf("Check the '%s' setting in your config file or use /config set %s <value>", e.Key, e.Key)
}

// ValidationError accumulates multiple field validation errors from the config.
type ValidationError struct {
	ErrList []string // One entry per validation failure
}

// Error returns all validation errors concatenated.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation errors (%d):\n%s", len(e.ErrList), strings.Join(e.ErrList, "\n"))
}

// Hint returns a generic validation hint.
func (e *ValidationError) Hint() string {
	return "Fix the listed configuration values and reload with /config reload or restart."
}

// Add appends a validation error message.
func (e *ValidationError) Add(msg string) {
	e.ErrList = append(e.ErrList, msg)
}

// HasErrors returns true if any validation errors were collected.
func (e *ValidationError) HasErrors() bool {
	return len(e.ErrList) > 0
}
