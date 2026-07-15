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

// TestEditEscapeRepro_NewContentPreservesLiteralBackslashN proves that a
// literal backslash-n sequence (e.g. a Go string escape "\n") supplied via
// new_content is written to the file verbatim, NOT converted into a real
// newline. This mirrors the real failure seen in session 1784126185 where the
// model edited internal/python/stdlib/re_test.go and the line
//
//	resultCode := code + "\n__result__ = str(_r)\n"
//
// was silently corrupted because the tool did strings.ReplaceAll(new_content, `\n`, "\n").
func TestEditEscapeRepro_NewContentPreservesLiteralBackslashN(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "snippet.go")
	original := "package main\n\nvar a = 1\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// JSON \\n -> literal backslash+n (the Go source escape we want in the file).
	// JSON \n  -> real newline (line separator).
	input := `{"path": "` + filePath + `", "operation": "replace_lines",` +
		` "start_line": 3, "end_line": 3,` +
		` "new_content": "\tresultCode := code + \"\\n__result__ = str(_r)\\n\""}`

	tool := &EditFileTool{ProjectDir: dir}
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("replace_lines failed: %v", err)
	}

	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	want := "resultCode := code + \"\\n__result__ = str(_r)\\n\""
	if !strings.Contains(string(got), want) {
		t.Fatalf("literal backslash-n was corrupted.\n want substring: %q\n got file:\n%s", want, string(got))
	}
}

// TestEditEscapeRepro_ReplacePatternStillWorks is a positive control:
// after removing the double-unescaping, a plain single-line replace_pattern
// still matches and replaces correctly.
func TestEditEscapeRepro_ReplacePatternStillWorks(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	input := `{"path": "` + filePath + `", "operation": "replace_pattern",` +
		` "pattern": "beta", "new_content": "BETA"}`

	tool := &EditFileTool{ProjectDir: dir}
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("replace_pattern failed: %v", err)
	}

	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "BETA") || strings.Contains(string(got), "beta") {
		t.Fatalf("replace_pattern did not replace correctly.\n got file:\n%s", string(got))
	}
}

// TestEditEscapeRepro_RealNewlinePatternRoutesToBlock proves that a pattern
// which genuinely spans multiple lines (a real newline introduced by JSON
// "\n", NOT a literal backslash-n) still routes to block matching. This guards
// against over-correcting: we removed the bogus double-unescaping, but genuine
// multi-line patterns must keep working.
func TestEditEscapeRepro_RealNewlinePatternRoutesToBlock(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "block.txt")
	original := "package main\n\nfunc oldName() {}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// JSON "func oldName() {}\n" carries a real trailing newline; the joined
	// block text is matched against the whole file via fuzzyEdit.
	input := `{"path": "` + filePath + `", "operation": "replace_pattern",` +
		` "pattern": "func oldName() {}\n", "new_content": "func newName() {}"}`

	tool := &EditFileTool{ProjectDir: dir}
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("replace_pattern failed: %v", err)
	}

	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "func newName()") || strings.Contains(string(got), "oldName") {
		t.Fatalf("multi-line block pattern did not match.\n got file:\n%s", string(got))
	}
}
