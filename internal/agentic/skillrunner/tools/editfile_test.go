// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

func TestNewEditFileTool(t *testing.T) {
	workDir := t.TempDir()
	logger := agentic.NewLogger(agentic.Error)

	tool := NewEditFileTool(workDir, logger)
	if tool == nil {
		t.Fatal("NewEditFileTool returned nil")
	}

	_ = tool
}

func TestEditFileToolSchema(t *testing.T) {
	workDir := t.TempDir()
	tool := NewEditFileTool(workDir, nil)

	schema := tool.Schema()
	if schema.Name != "edit" {
		t.Errorf("Schema.Name = %q, want %q", schema.Name, "edit")
	}
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if schema.Schema == nil {
		t.Error("Schema.Schema should not be nil")
	}

	// Check required fields
	required, ok := schema.Schema["required"].([]string)
	if !ok {
		t.Fatal("required field not found or wrong type")
	}
	if len(required) != 1 || required[0] != "path" {
		t.Errorf("required = %v, want [path]", required)
	}
}

func TestEditFileToolExecuteReplaceText(t *testing.T) {
	workDir := t.TempDir()
	testFile := createEditTestFile(t, workDir, "Hello World\nThis is a test\nGoodbye World")
	tool := NewEditFileTool(workDir, nil)

	for _, tt := range replaceTextCases() {
		t.Run(tt.name, func(t *testing.T) {
			if tt.reset {
				os.WriteFile(testFile, []byte("Hello World\nThis is a test\nGoodbye World"), 0644)
			}
			assertEditResult(t, tool, testFile, tt.input, tt.want, tt.wantError)
		})
	}
}

type editFileCase struct {
	name      string
	input     string
	want      string
	wantError bool
	reset     bool
}

func replaceTextCases() []editFileCase {
	return []editFileCase{
		{name: "replace_text", input: `{"path": "test.txt", "old_text": "Hello World", "new_text": "Hi Universe"}`, want: "Hi Universe\nThis is a test\nGoodbye World", reset: true},
		{name: "old_text_not_found", input: `{"path": "test.txt", "old_text": "Nonexistent", "new_text": "Replacement"}`, wantError: true, reset: true},
		{name: "replace_with_empty", input: `{"path": "test.txt", "old_text": "Hello World", "new_text": ""}`, want: "\nThis is a test\nGoodbye World", reset: true},
		{name: "path_traversal", input: `{"path": "../../../etc/passwd", "old_text": "x", "new_text": "y"}`, wantError: true},
		{name: "nonexistent_file", input: `{"path": "nonexistent.txt", "old_text": "x", "new_text": "y"}`, wantError: true},
	}
}

func createEditTestFile(t *testing.T, workDir, content string) string {
	t.Helper()
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	return testFile
}

func assertEditResult(t *testing.T, tool agentic.Tool, testFile, input, want string, wantError bool) {
	t.Helper()
	result, err := tool.Execute(input)
	if wantError {
		if err == nil {
			t.Error("Expected error, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result != "File edited successfully" {
		t.Errorf("Execute() returned %q, want %q", result, "File edited successfully")
	}
	assertFileContent(t, testFile, want)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != want {
		t.Errorf("File content = %q, want %q", string(content), want)
	}
}

func TestEditFileToolExecuteAddAfterLine(t *testing.T) {
	workDir := t.TempDir()
	testFile := createEditTestFile(t, workDir, "Line 1\nLine 2\nLine 3")
	tool := NewEditFileTool(workDir, nil)

	for _, tt := range addAfterLineCases() {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(testFile, []byte("Line 1\nLine 2\nLine 3"), 0644)
			assertEditResult(t, tool, testFile, tt.input, tt.want, tt.wantError)
		})
	}
}

func addAfterLineCases() []editFileCase {
	return []editFileCase{
		{name: "add_after_line", input: `{"path": "test.txt", "add_after_line": 2, "add_content": "New Line"}`, want: "Line 1\nLine 2\nNew Line\nLine 3"},
		{name: "add_after_line_exceeds", input: `{"path": "test.txt", "add_after_line": 100, "add_content": "New Line"}`, wantError: true},
		{name: "add_at_end", input: `{"path": "test.txt", "add_after_line": 3, "add_content": "New Line"}`, want: "Line 1\nLine 2\nLine 3\nNew Line"},
	}
}

func TestEditFileToolExecuteRemoveLine(t *testing.T) {
	workDir := t.TempDir()
	testFile := createEditTestFile(t, workDir, "Line 1\nLine 2\nLine 3")
	tool := NewEditFileTool(workDir, nil)

	for _, tt := range removeLineCases() {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(testFile, []byte("Line 1\nLine 2\nLine 3"), 0644)
			assertEditResult(t, tool, testFile, tt.input, tt.want, tt.wantError)
		})
	}
}

func removeLineCases() []editFileCase {
	return []editFileCase{
		{name: "remove_middle_line", input: `{"path": "test.txt", "remove_line": 2}`, want: "Line 1\nLine 3"},
		{name: "remove_first_line", input: `{"path": "test.txt", "remove_line": 1}`, want: "Line 2\nLine 3"},
		{name: "remove_last_line", input: `{"path": "test.txt", "remove_line": 3}`, want: "Line 1\nLine 2"},
		{name: "remove_line_exceeds", input: `{"path": "test.txt", "remove_line": 100}`, wantError: true},
	}
}

func TestEditFileToolExecuteNoOperation(t *testing.T) {
	workDir := t.TempDir()
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tool := NewEditFileTool(workDir, nil)

	// No valid operation specified
	input := `{"path": "test.txt"}`
	_, err := tool.Execute(input)
	if err == nil {
		t.Error("Expected error for no valid operation, got nil")
	}
}

func TestEditFileToolPathTraversal(t *testing.T) {
	workDir := t.TempDir()
	tool := NewEditFileTool(workDir, nil)

	traversalAttempts := []string{
		`{"path": "../outside.txt", "old_text": "x", "new_text": "y"}`,
		`{"path": "/etc/passwd", "old_text": "x", "new_text": "y"}`,
	}

	for _, input := range traversalAttempts {
		t.Run(input, func(t *testing.T) {
			_, err := tool.Execute(input)
			if err == nil {
				t.Errorf("Expected path traversal error for %q", input)
			}
		})
	}
}
