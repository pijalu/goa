// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tools covers the static/meta methods of tool implementations:
// Schema, ShortDoc, LongDoc, Examples, IsRetryable, and Access.
package tools

import (
	"errors"
	"testing"
)

// assertError is a simple error for testing error paths.

// ReadFileTool

func TestReadFileTool_Schema(t *testing.T) {
	tool := &ReadFileTool{}
	s := tool.Schema()
	if s.Name != "read" {
		t.Errorf("expected 'read', got %q", s.Name)
	}
}

func TestReadFileTool_IsRetryable(t *testing.T) {
	tool := &ReadFileTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected false")
	}
}

func TestReadFileTool_ShortDoc(t *testing.T) {
	tool := &ReadFileTool{}
	if tool.ShortDoc() == "" {
		t.Error("expected non-empty ShortDoc")
	}
}

func TestReadFileTool_LongDoc(t *testing.T) {
	tool := &ReadFileTool{}
	if tool.LongDoc() == "" {
		t.Error("expected non-empty LongDoc")
	}
}

func TestReadFileTool_Examples(t *testing.T) {
	tool := &ReadFileTool{}
	if len(tool.Examples()) == 0 {
		t.Error("expected at least one example")
	}
}

// WriteFileTool

func TestWriteFileTool_Schema(t *testing.T) {
	tool := &WriteFileTool{}
	s := tool.Schema()
	if s.Name != "write" {
		t.Errorf("expected 'write', got %q", s.Name)
	}
}

func TestWriteFileTool_IsRetryable(t *testing.T) {
	tool := &WriteFileTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected false")
	}
}

func TestWriteFileTool_ShortDoc(t *testing.T) {
	tool := &WriteFileTool{}
	if tool.ShortDoc() == "" {
		t.Error("expected non-empty ShortDoc")
	}
}

func TestWriteFileTool_LongDoc(t *testing.T) {
	tool := &WriteFileTool{}
	if tool.LongDoc() == "" {
		t.Error("expected non-empty LongDoc")
	}
}

func TestWriteFileTool_Examples(t *testing.T) {
	tool := &WriteFileTool{}
	if len(tool.Examples()) == 0 {
		t.Error("expected at least one example")
	}
}

// SearchTool

func TestSearchTool_IsRetryable(t *testing.T) {
	tool := &SearchTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected false")
	}
}

// PTYExecTool

func TestPTYExecTool_IsRetryable(t *testing.T) {
	tool := &PTYExecTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected false")
	}
}

// EditFileTool

func TestEditFileTool_Access(t *testing.T) {
	tool := &EditFileTool{}
	result := tool.Access(`{"path": "/tmp/test.go"}`)
	if len(result.WritePaths) == 0 {
		t.Error("expected non-empty WritePaths for valid input")
	}
}

// BashTool

func TestBashTool_Access(t *testing.T) {
	tool := &BashTool{}
	result := tool.Access("")
	if result.Category != "shell" {
		t.Errorf("expected category 'shell', got %q", result.Category)
	}
}

func TestBashTool_ExitCode_Zero(t *testing.T) {
	if code := exitCode(nil); code != 0 {
		t.Errorf("expected 0 for nil error, got %d", code)
	}
}

func TestBashTool_ExitCode_NonExecError(t *testing.T) {
	if code := exitCode(errors.New("some error")); code != -1 {
		t.Errorf("expected -1 for non-exec error, got %d", code)
	}
}
