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

// EditFileTool replacePattern helper tests

func TestEditFileTool_ReplacePattern_Basic(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"hello world", "foo bar", "hello again"}
	result, _, err := tool.replacePattern(lines, "hello", "", 1, []string{"hi"}, IndentAsIs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(result), result)
	}
	if result[0] != "hi" {
		t.Errorf("expected 'hi' (first occurrence), got %q", result[0])
	}
	if result[2] != "hello again" {
		t.Errorf("expected 'hello again' (second match not replaced), got %q", result[2])
	}
}

func TestEditFileTool_ReplacePattern_Occurrence(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"dup line", "other", "dup line"}
	result, _, err := tool.replacePattern(lines, "dup", "", 2, []string{"replaced"}, IndentAsIs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0] != "dup line" || result[2] != "replaced" {
		t.Errorf("expected only 2nd occurrence replaced, got: %v", result)
	}
}

func TestEditFileTool_ReplacePattern_NoMatch(t *testing.T) {
	tool := &EditFileTool{}
	_, _, err := tool.replacePattern([]string{"a", "b"}, "zzz", "", 1, []string{"x"}, IndentAsIs)
	if err == nil {
		t.Fatal("expected error for no match")
	}
	var toolErr *internal.ToolError
	if !errors.As(err, &toolErr) {
		t.Errorf("expected ToolError, got %T", err)
	}
}

func TestEditFileTool_ReplacePattern_EmptyPattern(t *testing.T) {
	tool := &EditFileTool{}
	_, _, err := tool.replacePattern([]string{"a"}, "", "", 1, []string{"b"}, IndentAsIs)
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestEditFileTool_ReplacePattern_WithFlags(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"HELLO", "world"}
	result, _, err := tool.replacePattern(lines, "hello", "i", 1, []string{"hi"}, IndentAsIs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0] != "hi" {
		t.Errorf("expected case-insensitive match, got: %v", result)
	}
}

// EditFileTool insertBefore helper tests

func TestEditFileTool_InsertBefore_WithLineNum(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"first", "second", "third"}
	result, _, err := tool.insertBefore(lines, 2, "", []string{"inserted"}, IndentAsIs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(result), result)
	}
	if result[1] != "inserted" {
		t.Errorf("expected 'inserted' at index 1, got: %v", result)
	}
}

func TestEditFileTool_InsertBefore_NoLineNumNoPattern(t *testing.T) {
	tool := &EditFileTool{}
	_, _, err := tool.insertBefore([]string{"a"}, 0, "", []string{"b"}, IndentAsIs)
	if err == nil {
		t.Fatal("expected error for missing params")
	}
}

// EditFileTool insertAfter helper tests

func TestEditFileTool_InsertAfter_WithLineNum(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"first", "second", "third"}
	result, _, err := tool.insertAfter(lines, 1, "", []string{"inserted"}, IndentAsIs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(result), result)
	}
	if result[1] != "inserted" {
		t.Errorf("expected 'inserted' at index 1, got: %v", result)
	}
}

func TestEditFileTool_InsertAfter_NoLineNumNoPattern(t *testing.T) {
	tool := &EditFileTool{}
	_, _, err := tool.insertAfter([]string{"a"}, 0, "", []string{"b"}, IndentAsIs)
	if err == nil {
		t.Fatal("expected error for missing params")
	}
}

// EditFileTool insertAtPattern tests

func TestEditFileTool_InsertAtPattern_Before(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"line1", "target", "line3"}
	result, _, err := tool.insertAtPattern(lines, "target", []string{"inserted"}, IndentAsIs, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(result), result)
	}
	if result[1] != "inserted" || result[2] != "target" {
		t.Errorf("expected 'inserted' before 'target', got: %v", result)
	}
}

func TestEditFileTool_InsertAtPattern_After(t *testing.T) {
	tool := &EditFileTool{}
	lines := []string{"line1", "target", "line3"}
	result, _, err := tool.insertAtPattern(lines, "target", []string{"inserted"}, IndentAsIs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[2] != "inserted" || result[1] != "target" {
		t.Errorf("expected 'inserted' after 'target', got: %v", result)
	}
}

func TestEditFileTool_InsertAtPattern_NoMatch(t *testing.T) {
	tool := &EditFileTool{}
	_, _, err := tool.insertAtPattern([]string{"a"}, "zzz", []string{"b"}, IndentAsIs, true)
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

// EditFileTool adjustIndent tests

func TestEditFileTool_AdjustIndent_Preserve(t *testing.T) {
	tool := &EditFileTool{}
	targetLines := []string{"    hello"}
	newLines := []string{"  world"}
	result := tool.adjustIndent(targetLines, newLines, IndentPreserve)
	if len(result) != 1 || result[0] != "    world" {
		t.Errorf("expected '    world', got %q", result[0])
	}
}

func TestEditFileTool_AdjustIndent_AsIs(t *testing.T) {
	tool := &EditFileTool{}
	targetLines := []string{"    hello"}
	newLines := []string{"  world"}
	result := tool.adjustIndent(targetLines, newLines, IndentAsIs)
	if result[0] != "  world" {
		t.Errorf("expected '  world' unchanged, got %q", result[0])
	}
}

func TestEditFileTool_AdjustIndent_Normalize(t *testing.T) {
	tool := &EditFileTool{}
	targetLines := []string{"    hello", "        nested"}
	newLines := []string{"  world"}
	result := tool.adjustIndent(targetLines, newLines, IndentNormalize)
	if result[0] != "    world" {
		t.Errorf("expected '    world' (normalized indent), got %q", result[0])
	}
}

func TestEditFileTool_AdjustIndent_EmptyTarget(t *testing.T) {
	tool := &EditFileTool{}
	result := tool.adjustIndent(nil, []string{"hello"}, IndentPreserve)
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("expected unchanged, got: %v", result)
	}
}

// adjustPreserve standalone tests

func TestAdjustPreserve_PositiveDelta(t *testing.T) {
	result := adjustPreserve([]string{"    line"}, []string{"text"})
	if result[0] != "    text" {
		t.Errorf("expected '    text', got %q", result[0])
	}
}

func TestAdjustPreserve_NegativeDelta(t *testing.T) {
	result := adjustPreserve([]string{"line"}, []string{"    text"})
	if result[0] != "text" {
		t.Errorf("expected 'text', got %q", result[0])
	}
}

func TestAdjustPreserve_EmptyInput(t *testing.T) {
	result := adjustPreserve(nil, []string{"hello"})
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("expected unchanged, got: %v", result)
	}
	result = adjustPreserve([]string{"line"}, nil)
	if result != nil {
		t.Errorf("expected nil for nil newLines, got: %v", result)
	}
}

// adjustNormalize standalone tests

func TestAdjustNormalize_Basic(t *testing.T) {
	targetLines := []string{"    hello"}
	newLines := []string{"  world"}
	result := adjustNormalize(targetLines, newLines)
	if result[0] != "    world" {
		t.Errorf("expected '    world', got %q", result[0])
	}
}

func TestAdjustNormalize_EmptyNewLine(t *testing.T) {
	result := adjustNormalize([]string{"  hello"}, []string{""})
	if len(result) != 1 || result[0] != "" {
		t.Errorf("expected empty string, got %q", result[0])
	}
}

func TestAdjustNormalize_MultipleNewLines(t *testing.T) {
	result := adjustNormalize([]string{"    a"}, []string{"  b", "  c"})
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if result[0] != "    b" || result[1] != "    c" {
		t.Errorf("expected normalized indents, got: %v", result)
	}
}

func TestAdjustNormalize_TargetTooDeep(t *testing.T) {
	targetLines := []string{"        a", "        b"}
	newLines := []string{"  c"}
	result := adjustNormalize(targetLines, newLines)
	if result[0] != "    c" {
		t.Errorf("expected '    c' (capped to 4), got %q", result[0])
	}
}

// leadingWS standalone tests

func TestLeadingWS_Spaces(t *testing.T) {
	if ws := leadingWS("    hello"); ws != "    " {
		t.Errorf("expected 4 spaces, got %q", ws)
	}
}

func TestLeadingWS_Tab(t *testing.T) {
	if ws := leadingWS("\thello"); ws != "\t" {
		t.Errorf("expected tab, got %q", ws)
	}
}

func TestLeadingWS_None(t *testing.T) {
	if ws := leadingWS("hello"); ws != "" {
		t.Errorf("expected empty, got %q", ws)
	}
}

// bash.go maskOutput tests (maskOutput replaces the mask string itself)

func TestMaskOutput_NoMatch(t *testing.T) {
	result := maskOutput("hello world", []string{"SECRET"})
	if result != "hello world" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestMaskOutput_ReplacesExact(t *testing.T) {
	result := maskOutput("API_KEY=sk-abc123", []string{"API_KEY"})
	if !strings.Contains(result, "***=sk-abc123") {
		t.Errorf("expected mask replaced, got %q", result)
	}
}

func TestMaskOutput_MultiplePatterns(t *testing.T) {
	result := maskOutput("SECRET=abc\nTOKEN=xyz", []string{"SECRET", "TOKEN"})
	if !strings.Contains(result, "***=abc") || !strings.Contains(result, "***=xyz") {
		t.Errorf("expected both masks replaced, got %q", result)
	}
}

// readfile.go tests

func TestReadFileTool_Access(t *testing.T) {
	tool := &ReadFileTool{}
	result := tool.Access(`{"path": "/tmp/test.go"}`)
	if len(result.ReadPaths) == 0 {
		t.Error("expected non-empty ReadPaths for valid input")
	}
}

func TestReadFileTool_Access_InvalidJSON(t *testing.T) {
	tool := &ReadFileTool{}
	result := tool.Access("{bad")
	if len(result.WritePaths) != 0 {
		t.Errorf("expected empty ReadPaths for invalid JSON, got %v", result.ReadPaths)
	}
}

// writefile.go tests

func TestWriteFileTool_Access(t *testing.T) {
	tool := &WriteFileTool{}
	result := tool.Access(`{"path": "/tmp/output.go"}`)
	if len(result.WritePaths) == 0 {
		t.Error("expected non-empty WritePaths for valid input")
	}
}

func TestWriteFileTool_Access_InvalidJSON(t *testing.T) {
	tool := &WriteFileTool{}
	result := tool.Access("{bad")
	if len(result.WritePaths) != 0 {
		t.Errorf("expected empty WritePaths for invalid JSON, got %v", result.WritePaths)
	}
}

// MementoTool globalMemoryDir

func TestMementoTool_GlobalMemoryDir(t *testing.T) {
	tool := &MementoTool{GlobalDir: "/custom/goa"}
	dir := tool.globalMemoryDir()
	if dir != "/custom/goa/memory" {
		t.Errorf("expected '/custom/goa/memory', got %q", dir)
	}
}
