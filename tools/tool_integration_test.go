// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

// BGExecTool tests

func TestBGExecTool_Schema(t *testing.T) {
	tool := &BGExecTool{}
	s := tool.Schema()
	if s.Name != "bg_exec" {
		t.Errorf("expected 'bg_exec', got %q", s.Name)
	}
}

func TestBGExecTool_IsRetryable(t *testing.T) {
	tool := &BGExecTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected false")
	}
}

func TestBGExecTool_ShortDoc(t *testing.T) {
	tool := &BGExecTool{}
	if tool.ShortDoc() == "" {
		t.Error("expected non-empty ShortDoc")
	}
}

func TestBGExecTool_LongDoc(t *testing.T) {
	tool := &BGExecTool{}
	if tool.LongDoc() == "" {
		t.Error("expected non-empty LongDoc")
	}
}

func TestBGExecTool_Examples(t *testing.T) {
	tool := &BGExecTool{}
	if len(tool.Examples()) == 0 {
		t.Error("expected at least one example")
	}
}

func TestBGExecTool_Execute_InvalidJSON(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute("{bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestBGExecTool_Execute_UnknownAction(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "fly"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestBGExecTool_Execute_StartMissingCommand(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "start"}`)
	if err == nil {
		t.Fatal("expected error for missing command")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
}

func TestBGExecTool_Execute_StatusNonExistent(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "status", "id": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestBGExecTool_Execute_ReadNonExistent(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "read", "id": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for reading nonexistent process")
	}
}

func TestBGExecTool_Execute_WriteNonExistent(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "write", "id": "nonexistent", "input": "hello"}`)
	if err == nil {
		t.Fatal("expected error for writing to nonexistent process")
	}
}

func TestBGExecTool_Execute_StopNonExistent(t *testing.T) {
	tool := NewBGExecTool()
	_, err := tool.Execute(`{"action": "stop", "id": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for stopping nonexistent process")
	}
}

func TestBGExecTool_Execute_ListEmpty(t *testing.T) {
	tool := NewBGExecTool()
	result, err := tool.Execute(`{"action": "list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No active processes") {
		t.Errorf("expected 'No active processes' message, got: %s", result)
	}
}

// EditFileTool helper tests

func TestEditFileTool_ErrProtected(t *testing.T) {
	tool := &EditFileTool{}
	err := tool.errProtected("/etc/passwd")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T: %v", err, err)
	}
	if toolErr.Type != "protected_path" {
		t.Errorf("expected type 'protected_path', got %q", toolErr.Type)
	}
}

func TestEditFileTool_ErrWrite(t *testing.T) {
	tool := &EditFileTool{}
	err := tool.errWrite("/tmp/readonly", errors.New("permission denied"))
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("expected write in error, got: %v", err)
	}
}

func TestMatchLine_Regex(t *testing.T) {
	if !matchLine("hello world", "hello", true) {
		t.Error("expected regex match")
	}
}

func TestMatchLine_Substring(t *testing.T) {
	if !matchLine("abcdef", "bcd", true) {
		t.Error("expected substring match")
	}
}

func TestMatchLine_NoMatch(t *testing.T) {
	if matchLine("abc", "xyz", true) {
		t.Error("expected no match")
	}
}

func TestMatchLine_CaseInsensitive(t *testing.T) {
	if !matchLine("HELLO", "hello", false) {
		t.Error("expected case-insensitive match")
	}
}

func TestMatchLine_InvalidRegexFallback(t *testing.T) {
	if !matchLine("hello (world", "(world", true) {
		t.Error("expected substring fallback match")
	}
}

// bash_renderer helper tests

func TestBashRenderer_PreviewLines(t *testing.T) {
	r := &BashRenderer{}
	if n := r.PreviewLines(); n <= 0 {
		t.Errorf("expected positive preview lines, got %d", n)
	}
}

func TestBashRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := &BashRenderer{}
	r.HideResultWhenCollapsed() // just verify no panic
}

// readfile_renderer helper tests

func TestReadFileRenderer_PreviewLines(t *testing.T) {
	r := &ReadFileRenderer{}
	if n := r.PreviewLines(); n <= 0 {
		t.Errorf("expected positive preview lines, got %d", n)
	}
}

func TestReadFileRenderer_ShowsMetadataWhenCollapsed(t *testing.T) {
	r := &ReadFileRenderer{}
	// Always show metadata (path, offset, size) even when collapsed.
	// File content is never displayed in the TUI — only the agent sees it.
	if r.HideResultWhenCollapsed() {
		t.Error("read should show metadata when collapsed")
	}
}

// writefile_renderer helper tests

func TestWriteFileRenderer_PreviewLines(t *testing.T) {
	r := &WriteFileRenderer{}
	if n := r.PreviewLines(); n <= 0 {
		t.Errorf("expected positive preview lines, got %d", n)
	}
}

func TestWriteFileRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := &WriteFileRenderer{}
	r.HideResultWhenCollapsed() // just verify no panic
}

// renderer_common helpers

func TestFormatPathRelativeToCwd(t *testing.T) {
	result := formatPathRelativeToCwdOrAbsolute("/absolute/path/file.go", "/absolute/path")
	if result != "file.go" {
		t.Errorf("expected 'file.go', got %q", result)
	}
}

func TestFormatPathRelativeToCwd_Negative(t *testing.T) {
	result := formatPathRelativeToCwdOrAbsolute("/other/path/file.go", "/absolute/path")
	if result != "/other/path/file.go" {
		t.Errorf("expected absolute path, got %q", result)
	}
}

func TestHighlightPython(t *testing.T) {
	result := highlightPython("print('hello')")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightJSON(t *testing.T) {
	result := highlightJSON(`{"key": "value"}`)
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightYAML(t *testing.T) {
	result := highlightYAML("key: value\n")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestIsHexDigit(t *testing.T) {
	if !isHexDigit('a') {
		t.Error("expected 'a' to be hex digit")
	}
	if !isHexDigit('F') {
		t.Error("expected 'F' to be hex digit")
	}
	if !isHexDigit('0') {
		t.Error("expected '0' to be hex digit")
	}
	if isHexDigit('g') {
		t.Error("expected 'g' not to be hex digit")
	}
}

func TestKeyHint(t *testing.T) {
	result := keyHint("enter", "confirm")
	if result == "" {
		t.Error("expected non-empty key hint")
	}
}

func TestExpandHint(t *testing.T) {
	result := expandHint(5, "Ctrl+O")
	if result == "" {
		t.Error("expected non-empty expand hint")
	}
}

// search tool tests

func TestSearchTool_ShouldSkipDir(t *testing.T) {
	tool := &SearchTool{}
	excludes := []string{".git", "node_modules", ".DS_Store"}
	if !tool.shouldSkipDir(".git", excludes) {
		t.Error("expected .git to be skipped")
	}
	if !tool.shouldSkipDir("node_modules", excludes) {
		t.Error("expected node_modules to be skipped")
	}
	if tool.shouldSkipDir("src", excludes) {
		t.Error("expected src not to be skipped")
	}
}
