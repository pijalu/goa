// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"errors"
	"strings"
	"testing"
)

// TestToolError verifies ToolError formatting and Hint behavior.
func TestToolError(t *testing.T) {
	err := &ToolError{
		Tool:     "read",
		Type:     "file_not_found",
		Detail:   "File not found: test.txt",
		HintText: "Check the file path and try again.",
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "[read error: file_not_found]") {
		t.Errorf("Error missing tool header: %s", errStr)
	}
	if !strings.Contains(errStr, "File not found") {
		t.Errorf("Error missing detail: %s", errStr)
	}
	if !strings.Contains(errStr, "Hint:") {
		t.Errorf("Error missing Hint prefix: %s", errStr)
	}
	if !strings.Contains(errStr, err.HintText) {
		t.Errorf("Error missing hint text: %s", errStr)
	}

	hint := err.Hint()
	if hint != err.HintText {
		t.Errorf("Hint() = %q, want %q", hint, err.HintText)
	}
}

// TestToolErrorEmptyHint verifies an empty hint is omitted from output.
func TestToolErrorEmptyHint(t *testing.T) {
	err := &ToolError{
		Tool:   "bash",
		Type:   "timeout",
		Detail: "Command timed out",
	}
	errStr := err.Error()
	if strings.Contains(errStr, "Hint:") {
		t.Errorf("Empty hint should be omitted, got: %s", errStr)
	}
}

// TestToolErrorNoTool verifies error with empty tool name.
func TestToolErrorNoTool(t *testing.T) {
	err := &ToolError{
		Type:   "generic",
		Detail: "Something went wrong",
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "[ error: generic]") {
		t.Errorf("Empty tool should render as empty: %s", errStr)
	}
}

// TestConfigError verifies ConfigError wrapping and hint.
func TestConfigError(t *testing.T) {
	inner := errors.New("invalid value")
	err := &ConfigError{
		Key: "execution.mode",
		Err: inner,
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "execution.mode") {
		t.Errorf("Error missing key: %s", errStr)
	}
	if !strings.Contains(errStr, "invalid value") {
		t.Errorf("Error missing inner error: %s", errStr)
	}

	if !errors.Is(err, inner) {
		t.Error("ConfigError should unwrap to inner error")
	}

	hint := err.Hint()
	if !strings.Contains(hint, "execution.mode") {
		t.Errorf("Hint should mention the config key: %s", hint)
	}
}

// TestValidationError verifies ValidationError accumulation and hint.
func TestValidationError(t *testing.T) {
	ve := &ValidationError{}
	if ve.HasErrors() {
		t.Error("New ValidationError should not have errors")
	}

	ve.Add("invalid execution mode")
	ve.Add("missing provider")

	if !ve.HasErrors() {
		t.Error("ValidationError should have errors after Add")
	}

	errStr := ve.Error()
	if !strings.Contains(errStr, "2") {
		t.Errorf("Error should mention count 2: %s", errStr)
	}
	if !strings.Contains(errStr, "invalid execution mode") {
		t.Errorf("Error missing first message: %s", errStr)
	}
	if !strings.Contains(errStr, "missing provider") {
		t.Errorf("Error missing second message: %s", errStr)
	}

	hint := ve.Hint()
	if hint == "" {
		t.Error("Hint should not be empty")
	}
}

// TestValidationErrorEmpty verifies empty ValidationError.
func TestValidationErrorEmpty(t *testing.T) {
	ve := &ValidationError{}
	if ve.HasErrors() {
		t.Error("Empty ValidationError.HasErrors should be false")
	}
	errStr := ve.Error()
	if !strings.Contains(errStr, "0") {
		t.Errorf("Empty error should show 0: %s", errStr)
	}
}
