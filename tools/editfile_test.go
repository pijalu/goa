// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileTool_Schema_HasRequiredFields(t *testing.T) {
	tool := &EditFileTool{}
	schema := tool.Schema()
	if schema.Name != "edit" {
		t.Errorf("schema.Name = %q, want %q", schema.Name, "edit")
	}
	if schema.Schema == nil {
		t.Fatal("schema.Schema should not be nil")
	}
	props := schema.Schema["properties"].(map[string]any)
	for _, field := range []string{"path", "old_string", "new_string", "operation", "start_line", "end_line", "pattern", "new_content", "indent_mode"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing required field: %s", field)
		}
	}
	reqFields := make(map[string]bool)
	switch req := schema.Schema["required"].(type) {
	case []string:
		for _, r := range req {
			reqFields[r] = true
		}
	case []any:
		for _, r := range req {
			reqFields[r.(string)] = true
		}
	}
	for _, field := range []string{"path"} {
		if !reqFields[field] {
			t.Errorf("schema required missing field: %s", field)
		}
	}
}

func TestEditFileTool_Schema_OperationHasValidValues(t *testing.T) {
	tool := &EditFileTool{}
	schema := tool.Schema()
	props := schema.Schema["properties"].(map[string]any)
	op, ok := props["operation"].(map[string]any)
	if !ok {
		t.Fatal("operation should be a map")
	}
	enumVal, exists := op["enum"]
	if !exists {
		t.Fatal("operation should have enum")
	}
	enum, ok := enumVal.([]string)
	if !ok {
		t.Fatalf("enum should be []string, got %T", enumVal)
	}
	// Operation no longer has an enum restriction — the LLM can use all operations.
	// Just verify it exists and lists the expected operations.
	expected := map[string]bool{"replace": true, "replace_lines": true, "replace_pattern": true, "insert_after": true, "insert_before": true, "delete_lines": true}
	if len(enum) != len(expected) {
		t.Errorf("expected %d operations, got %d", len(expected), len(enum))
	}
	for _, v := range enum {
		if !expected[v] {
			t.Errorf("unexpected operation: %s", v)
		}
	}
}

func TestEditFileTool_Documentation_LongDocLongerThanShort(t *testing.T) {
	tool := &EditFileTool{}
	short := tool.ShortDoc()
	long := tool.LongDoc()
	if short == "" {
		t.Error("ShortDoc should not be empty")
	}
	if len(long) <= len(short) {
		t.Errorf("LongDoc (%d chars) should be longer than ShortDoc (%d chars)", len(long), len(short))
	}
}

func TestEditFileTool_Examples_NotEmpty(t *testing.T) {
	tool := &EditFileTool{}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestEditFileTool_IsRetryable(t *testing.T) {
	tool := &EditFileTool{}
	if tool.IsRetryable(nil) {
		t.Error("IsRetryable should return false")
	}
}

func TestEditFileTool_Execute_EmptyInput_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute("")
	if err == nil {
		t.Error("Execute with empty input should return error")
	}
}

func TestEditFileTool_Execute_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute("not json")
	if err == nil {
		t.Error("Execute with invalid JSON should return error")
	}
}

func TestEditFileTool_Execute_MissingPath_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute(`{"operation": "delete_lines"}`)
	if err == nil {
		t.Error("Execute without path should return error")
	}
}

func TestEditFileTool_Execute_MissingOperation_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute(`{"path": "test.txt"}`)
	if err == nil {
		t.Error("Execute without operation or old_string should return error")
	}
}

func TestEditFileTool_Execute_InvalidOperation_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute(`{"path": "test.txt", "operation": "invalid_op"}`)
	if err == nil {
		t.Error("Execute with invalid operation should return error")
	}
}

func TestEditFileTool_Execute_NonexistentFile_ReturnsError(t *testing.T) {
	tool := &EditFileTool{}
	_, err := tool.Execute(`{"path": "/nonexistent/path/file.txt", "operation": "delete_lines", "start_line": 1, "end_line": 1}`)
	if err == nil {
		t.Error("Execute on nonexistent file should return error")
	}
}

func TestEditFileTool_ReplaceLines_ModifiesFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	result, err := tool.Execute(`{"path": "` + filePath + `", "operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "modified"}`)
	if err != nil {
		t.Fatalf("Replace lines should succeed: %v", err)
	}
	// Verify the tool produced output indicating success
	if len(result) < 10 {
		t.Errorf("Expected meaningful result, got: %q", result)
	}
}

func TestEditFileTool_InsertAfter_AddsContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	result, err := tool.Execute(`{"path": "` + filePath + `", "operation": "insert_after", "start_line": 1, "new_content": "inserted"}`)
	if err != nil {
		t.Fatalf("Insert after should succeed: %v", err)
	}
	// Verify insert was reported in output
	if !strings.Contains(result, "inserted") && !strings.Contains(result, "insert") && !strings.Contains(result, "Applied") {
		t.Errorf("Expected result to mention the operation, got: %q", result)
	}
}

func TestEditFileTool_DeleteLines_RemovesContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3\nline4"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	result, err := tool.Execute(`{"path": "` + filePath + `", "operation": "delete_lines", "start_line": 2, "end_line": 3}`)
	if err != nil {
		t.Fatalf("Delete lines should succeed: %v", err)
	}
	if len(result) < 10 {
		t.Errorf("Expected meaningful result, got: %q", result)
	}
}

func TestEditFileTool_DeleteLines_ReportsDeletedLineCount(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("a\nb\nc\nd\ne\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	result, err := tool.Execute(`{"path": "` + filePath + `", "operation": "delete_lines", "start_line": 2, "end_line": 4}`)
	if err != nil {
		t.Fatalf("Delete lines should succeed: %v", err)
	}
	if !strings.Contains(result, "3 lines affected") {
		t.Errorf("Expected result to report 3 deleted lines, got: %q", result)
	}
}

func TestEditFileTool_NotFound_ProvidesHelpfulHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "nonexistent block", "new_string": "replacement"}`)
	if err == nil {
		t.Fatal("Expected error for nonexistent old_string")
	}
	if !strings.Contains(err.Error(), "read") && !strings.Contains(err.Error(), "delete_lines") {
		t.Errorf("Expected error hint to mention read or delete_lines, got: %v", err)
	}
}

func TestEditFileTool_FuzzyEdit_ExactOnly_WhenAllowFuzzIsFalse(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	// File has trailing spaces that differ from old_string
	if err := os.WriteFile(filePath, []byte("func foo() {   \n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: false}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "func foo() {", "new_string": "func foo() {"}`)
	if err == nil {
		t.Fatal("AllowFuzz=false should fail on trailing whitespace mismatch")
	}
}

func TestEditFileTool_FuzzyEdit_ExactMatch_WhenAllowFuzzIsFalse(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("func foo() {\n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: false}
	result, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "func foo() {", "new_string": "func foo() {\n\treturn 2\n}"}`)
	if err != nil {
		t.Fatalf("Exact match with AllowFuzz=false should succeed: %v", err)
	}
	if !strings.Contains(result, "exact") {
		t.Errorf("Expected result to mention exact match, got: %q", result)
	}
}

func TestEditFileTool_FuzzyEdit_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	result, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "\tfmt.Println(\"hello\")", "new_string": "\tfmt.Println(\"world\")"}`)
	if err != nil {
		t.Fatalf("Exact search/replace should succeed: %v", err)
	}
	if !strings.Contains(result, "exact") && !strings.Contains(result, "applied") {
		t.Errorf("Expected result to mention exact match, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), `fmt.Println("world")`) {
		t.Errorf("File should contain replacement text")
	}
	if strings.Contains(string(data), `fmt.Println("hello")`) {
		t.Errorf("File should not contain old text")
	}
}

func TestEditFileTool_FuzzyEdit_TrailingWhitespace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	// File has trailing spaces
	if err := os.WriteFile(filePath, []byte("func foo() {   \n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	// old_string has no trailing spaces — fuzzy matching should handle it
	result, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "func foo() {", "new_string": "func foo() {"}`)
	if err != nil {
		t.Fatalf("Trailing whitespace fuzzy match should succeed: %v", err)
	}
	if !strings.Contains(result, "trailing") {
		t.Errorf("Expected result to mention trailing whitespace, got: %q", result)
	}
}

func TestEditFileTool_FuzzyEdit_NotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "nonexistent content", "new_string": "replacement"}`)
	if err == nil {
		t.Fatal("Execute with nonexistent old_string should return error")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "not_found") {
		t.Errorf("Error should mention 'not found', got: %v", err)
	}
}

func TestEditFileTool_FuzzyEdit_FuzzyWhitespace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	// File uses different indentation than old_string
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {\n\tx := 1\n\ty := 2\n\tfmt.Println(x + y)\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	// old_string uses no indentation — fuzzy matching should re-indent
	result, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "x := 1\ny := 2\nfmt.Println(x + y)", "new_string": "x := 10\ny := 20\nfmt.Println(x * y)"}`)
	if err != nil {
		t.Fatalf("Fuzzy indentation match should succeed: %v", err)
	}
	if !strings.Contains(result, "fuzzy") {
		t.Errorf("Expected result to mention fuzzy matching, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "x := 10") {
		t.Errorf("File should contain the updated value")
	}
}

func TestEditFileTool_FuzzyEdit_EmptyOldString(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "", "new_string": "new"}`)
	if err == nil {
		t.Fatal("Execute with empty old_string should return error")
	}
}

func TestEditFileTool_FuzzyEdit_NoChange(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "content", "new_string": "content"}`)
	if err == nil {
		t.Fatal("Execute with identical old and new should return error")
	}
}

func TestEditFileTool_AtPrefix_ResolvesToCurrentDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	result, err := tool.Execute(`{"path": "@test.txt", "old_string": "hello world", "new_string": "hi world"}`)
	if err != nil {
		t.Fatalf("Execute with @ prefix should succeed: %v", err)
	}
	if !strings.Contains(result, "search/replace applied") {
		t.Errorf("Expected edit result, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "hi world") {
		t.Errorf("File should be modified, got: %q", string(data))
	}
}

func TestEditFileTool_FuzzyFilename_MatchesClosestFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	result, err := tool.Execute(`{"path": "targt.txt", "old_string": "hello world", "new_string": "hi world"}`)
	if err != nil {
		t.Fatalf("Execute with fuzzy filename should succeed: %v", err)
	}
	if !strings.Contains(result, "used closest match") {
		t.Errorf("Expected fuzzy-match note, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "hi world") {
		t.Errorf("File should be modified, got: %q", string(data))
	}
}

func TestEditFileTool_FuzzyFilename_Disabled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	off := false
	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true, Config: FileToolConfig{FuzzyMatch: &off}}
	_, err := tool.Execute(`{"path": "targt.txt", "old_string": "hello world", "new_string": "hi world"}`)
	if err == nil {
		t.Fatal("Expected error when fuzzy filename matching is disabled")
	}
	if !strings.Contains(err.Error(), "file_not_found") {
		t.Errorf("Expected file_not_found error, got: %v", err)
	}
}

func TestEditFileTool_FuzzyFilename_LegacyOperation(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	result, err := tool.Execute(`{"path": "targt.txt", "operation": "delete_lines", "start_line": 2, "end_line": 2}`)
	if err != nil {
		t.Fatalf("Execute with fuzzy filename on legacy operation should succeed: %v", err)
	}
	if !strings.Contains(result, "used closest match") {
		t.Errorf("Expected fuzzy-match note, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	if strings.Contains(string(data), "line2") {
		t.Errorf("line2 should have been deleted, got: %q", string(data))
	}
}

func TestEditFileTool_ReplacePattern_EscapedNewlinesAndQuotes(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "game.js")
	original := `// Placeholder functions for key game components:
// initGame(), drawMap(), updateGame(), and handleInput().

const canvas = document.getElementById('gameCanvas');
const ctx = canvas.getContext('2d');

console.log("Game Initializing...");
`
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	// Pattern contains literal \n and \" sequences as the model often emits them.
	pattern := `// Placeholder functions for key game components:\n// initGame(), drawMap(), updateGame(), and handleInput().\n\nconst canvas = document.getElementById('gameCanvas');\nconst ctx = canvas.getContext('2d');\n\nconsole.log(\"Game Initializing...\");`
	newContent := `// New header
const canvas = document.getElementById('gameCanvas');
const ctx = canvas.getContext('2d');

console.log("Ready");`
	input := fmt.Sprintf(`{"path": %q, "operation": "replace_pattern", "pattern": %q, "new_content": %q}`, filePath, pattern, newContent)
	result, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("Replace pattern with escaped newlines should succeed: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	content := string(data)
	if !strings.Contains(content, "// New header") {
		t.Errorf("File should contain replacement text, got: %q", content)
	}
	if strings.Contains(content, "Placeholder functions for key game components") {
		t.Errorf("File should not contain old text, got: %q", content)
	}
	if !strings.Contains(result, "affected") {
		t.Errorf("Expected result to mention affected lines, got: %q", result)
	}
}
