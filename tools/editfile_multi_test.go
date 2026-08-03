// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileTool_Schema_HasEditsProperty(t *testing.T) {
	tool := &EditFileTool{}
	props := tool.Schema().Schema["properties"].(map[string]any)
	edits, ok := props["edits"].(map[string]any)
	if !ok {
		t.Fatal("schema missing 'edits' property")
	}
	if edits["type"] != "array" {
		t.Errorf("edits.type = %v, want array", edits["type"])
	}
	items, ok := edits["items"].(map[string]any)
	if !ok {
		t.Fatal("edits missing 'items' schema")
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("edits.items missing 'properties'")
	}
	for _, field := range []string{"operation", "old_string", "new_string", "start_line", "end_line", "pattern", "pattern_flags", "occurrence", "new_content", "indent_mode"} {
		if _, ok := itemProps[field]; !ok {
			t.Errorf("edits.items.properties missing field: %s", field)
		}
	}
}

func TestEditFileTool_MultiEdit_AppliesAllEditsInOrder(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	result, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "alpha", "new_string": "ALPHA"},
		{"old_string": "gamma", "new_string": "GAMMA"}
	]}`)
	if err != nil {
		t.Fatalf("multi-edit should succeed: %v", err)
	}
	if !strings.Contains(result, "2 edits applied") {
		t.Errorf("result should report the edit count, got: %q", result)
	}
	data, _ := os.ReadFile(filePath)
	want := "ALPHA\nbeta\nGAMMA\n"
	if string(data) != want {
		t.Errorf("file content = %q, want %q", string(data), want)
	}
}

func TestEditFileTool_MultiEdit_LaterEditsSeeUpdatedContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "one\ntwo\nthree\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	// Edit 1 replaces line 1 with two lines, shifting every later line number
	// by one. Edit 2 targets start_line 4 — only valid against the content
	// produced by edit 1.
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "one\none-and-a-half"},
		{"operation": "replace_lines", "start_line": 4, "end_line": 4, "new_content": "THREE"}
	]}`)
	if err != nil {
		t.Fatalf("multi-edit should succeed: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	want := "one\none-and-a-half\ntwo\nTHREE\n"
	if string(data) != want {
		t.Errorf("file content = %q, want %q", string(data), want)
	}
}

func TestEditFileTool_MultiEdit_MixedOperations(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	original := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "println(\"hi\")", "new_string": "println(\"hello\")"},
		{"operation": "insert_after", "pattern": "func main() {", "new_content": "\t// entry point", "indent_mode": "as-is"},
		{"operation": "delete_lines", "start_line": 2, "end_line": 2}
	]}`)
	if err != nil {
		t.Fatalf("mixed multi-edit should succeed: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	want := "package main\nfunc main() {\n\t// entry point\n\tprintln(\"hello\")\n}\n"
	if string(data) != want {
		t.Errorf("file content = %q, want %q", string(data), want)
	}
}

func TestEditFileTool_MultiEdit_AtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "alpha", "new_string": "ALPHA"},
		{"old_string": "does-not-exist", "new_string": "X"},
		{"old_string": "gamma", "new_string": "GAMMA"}
	]}`)
	if err == nil {
		t.Fatal("multi-edit with a failing edit should return an error")
	}
	if !strings.Contains(err.Error(), "edit 2/3") {
		t.Errorf("error should identify the failing edit position, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no changes were written") {
		t.Errorf("error should state the atomicity guarantee, got: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != original {
		t.Errorf("file must be untouched after a failed batch, got: %q", string(data))
	}
}

func TestEditFileTool_MultiEdit_NotFoundErrorIncludesMatchStats(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "beta\ndrifted line", "new_string": "X"}
	]}`)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	// Same rich diagnostic as the single-edit path: matched/total line counts.
	if !strings.Contains(err.Error(), "1/2 lines") {
		t.Errorf("error should include line-match stats, got: %v", err)
	}
}

func TestEditFileTool_MultiEdit_FailingLineOpReportsPosition(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "one\ntwo\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"operation": "delete_lines", "start_line": 1, "end_line": 1},
		{"operation": "replace_lines", "start_line": 99, "end_line": 99, "new_content": "X"}
	]}`)
	if err == nil {
		t.Fatal("expected range error")
	}
	if !strings.Contains(err.Error(), "edit 2/2") {
		t.Errorf("error should identify the failing edit position, got: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != original {
		t.Errorf("file must be untouched after a failed batch, got: %q", string(data))
	}
}

func TestEditFileTool_MultiEdit_MissingOperationInElement(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [{"start_line": 1}]}`)
	if err == nil {
		t.Fatal("expected error for edit element without operation")
	}
	if !strings.Contains(err.Error(), "operation") {
		t.Errorf("error should mention the missing operation, got: %v", err)
	}
}

func TestEditFileTool_MultiEdit_SingleEditMatchesSinglePathContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "hello world\nsecond line\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	if _, err := tool.Execute(`{"path": "` + filePath + `", "edits": [{"old_string": "hello world", "new_string": "hello goa"}]}`); err != nil {
		t.Fatalf("single-element batch failed: %v", err)
	}
	batchData, _ := os.ReadFile(filePath)

	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"path": "` + filePath + `", "old_string": "hello world", "new_string": "hello goa"}`); err != nil {
		t.Fatalf("single-edit path failed: %v", err)
	}
	singleData, _ := os.ReadFile(filePath)

	if string(batchData) != string(singleData) {
		t.Errorf("batch and single-edit paths must produce identical content:\nbatch:  %q\nsingle: %q", batchData, singleData)
	}
}

func TestEditFileTool_MultiEdit_WritesFileOnce(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("a\nb\nc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var notified []string
	tool := &EditFileTool{
		ProjectDir:         dir,
		AllowFuzz:          true,
		FileChangeNotifier: func(path string) { notified = append(notified, path) },
	}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "a", "new_string": "A"},
		{"old_string": "b", "new_string": "B"},
		{"old_string": "c", "new_string": "C"}
	]}`)
	if err != nil {
		t.Fatalf("multi-edit failed: %v", err)
	}
	if len(notified) != 1 {
		t.Errorf("FileChangeNotifier fired %d times, want exactly 1 (single write)", len(notified))
	}
}

func TestEditFileTool_MultiEdit_FuzzyMatchReported(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	// Trailing whitespace after "alpha" forces the trailing-whitespace tier.
	original := "alpha  \nbeta\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	result, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "alpha", "new_string": "ALPHA"}
	]}`)
	if err != nil {
		t.Fatalf("multi-edit failed: %v", err)
	}
	if !strings.Contains(result, "trailing whitespace normalized") {
		t.Errorf("result should report the fuzzy match tier, got: %q", result)
	}
}
