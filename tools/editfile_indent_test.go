// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file pins the Issue-4 contract: `insert_after`, `insert_before` and
// `replace_lines` write caller-supplied `new_content` BYTE-FOR-BYTE unless an
// explicit `indent_mode` says otherwise. The previous default (`preserve`)
// padded the content to the target line's indent, which silently re-nests
// Markdown blocks (list items, blockquotes, code fences) where leading
// whitespace is semantic. `replace_pattern` keeps the legacy `preserve`
// default (its block path matches whole regions rather than a lone insertion).
//
// Every case asserts the whole file content byte-for-byte — not a substring —
// so trailing whitespace, added indentation and trailing-newline handling are
// all pinned.

// TestDefaultIndentMode pins the resolution table for the helper shared by both
// call sites (single-edit `editByOperation` and batch `applySingleEdit`).
func TestDefaultIndentMode(t *testing.T) {
	cases := []struct {
		name string
		op   EditOperation
		raw  string
		want IndentMode
	}{
		{"insert_after defaults to as-is", OpInsertAfter, "", IndentAsIs},
		{"insert_before defaults to as-is", OpInsertBefore, "", IndentAsIs},
		{"replace_lines defaults to as-is", OpReplaceLines, "", IndentAsIs},
		{"replace_pattern keeps preserve default (block path)", OpReplacePattern, "", IndentPreserve},
		{"delete_lines keeps preserve default", OpDeleteLines, "", IndentPreserve},
		{"replace keeps preserve default", OpReplace, "", IndentPreserve},
		{"explicit preserve wins over as-is default", OpInsertAfter, "preserve", IndentPreserve},
		{"explicit normalize wins over as-is default", OpReplaceLines, "normalize", IndentNormalize},
		{"explicit as-is wins over preserve default", OpReplacePattern, "as-is", IndentAsIs},
		{"explicit as-is on insert is preserved", OpInsertBefore, "as-is", IndentAsIs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultIndentMode(tc.op, tc.raw); got != tc.want {
				t.Errorf("defaultIndentMode(%q, %q) = %q, want %q", tc.op, tc.raw, got, tc.want)
			}
		})
	}
}

type indentCase struct {
	name    string
	content string
	// payload is the flat single-edit JSON body (without the path field).
	payload string
	want    string // exact expected file content on success
	wantErr string // substring expected in the error message
}

func runIndentCases(t *testing.T, cases []indentCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runIndentCase(t, tc)
		})
	}
}

// runIndentCase applies one flat single-edit payload and asserts the resulting
// file content byte-for-byte (or that the error fired and nothing was written).
func runIndentCase(t *testing.T, tc indentCase) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(filePath, []byte(tc.content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", ` + tc.payload + `}`)
	got := readIndentTestFile(t, filePath)
	if tc.wantErr != "" {
		assertIndentError(t, err, tc, got)
		return
	}
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if got != tc.want {
		t.Errorf("file content mismatch\n got %q\nwant %q", got, tc.want)
	}
}

func assertIndentError(t *testing.T, err error, tc indentCase, got string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil (content: %q)", tc.wantErr, got)
	}
	if !strings.Contains(err.Error(), tc.wantErr) {
		t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
	}
	if got != tc.content {
		t.Errorf("file must be untouched on error\n got %q\nwant %q", got, tc.content)
	}
}

func readIndentTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestEditFileTool_Issue4_DefaultAsIs reproduces the three Issue-4 corruptions:
// each supplied paragraph/bullet sat at a smaller indent than its target line,
// so the old `preserve` default padded it and re-nested the Markdown block.
func TestEditFileTool_Issue4_DefaultAsIs(t *testing.T) {
	cases := []indentCase{
		{
			name: "repro 1: paragraph supplied at column 0 stays at column 0 after an indented list item",
			content: "1. First item\n" +
				"   Continuation line\n" +
				"2. Second item\n",
			payload: `"operation": "insert_after", "start_line": 2, "new_content": "An unindented paragraph."`,
			want: "1. First item\n" +
				"   Continuation line\n" +
				"An unindented paragraph.\n" +
				"2. Second item\n",
		},
		{
			name: "repro 2: 3-space blockquote keeps 3 spaces (not 8) after an 8-space target",
			content: "- Guardrails\n" +
				"\n" +
				"        - nested note\n" +
				"- Next\n",
			payload: `"operation": "insert_after", "start_line": 3, "new_content": "   > \u26a0\ufe0f three spaces"`,
			want: "- Guardrails\n" +
				"\n" +
				"        - nested note\n" +
				"   > \u26a0\ufe0f three spaces\n" +
				"- Next\n",
		},
		{
			name: "repro 3: column-0 bullet replaced into a 2-space indented line stays at column 0",
			content: "- **Sandboxing**\n" +
				"  - child bullet\n" +
				"  - **Connectivity**\n" +
				"- tail\n",
			payload: `"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "- **Connectivity (network)**"`,
			want: "- **Sandboxing**\n" +
				"  - child bullet\n" +
				"- **Connectivity (network)**\n" +
				"- tail\n",
		},
		{
			name: "insert_before below an indented target also stays byte-for-byte",
			content: "- top\n" +
				"    - deep\n" +
				"- bottom\n",
			payload: `"operation": "insert_before", "start_line": 2, "new_content": "plain line"`,
			want: "- top\n" +
				"plain line\n" +
				"    - deep\n" +
				"- bottom\n",
		},
		{
			name: "code fence content with meaningful leading spaces is untouched",
			content: "text\n" +
				"        indented block\n",
			payload: `"operation": "insert_after", "start_line": 2, "new_content": "  two spaces kept"`,
			want: "text\n" +
				"        indented block\n" +
				"  two spaces kept\n",
		},
	}
	runIndentCases(t, cases)
}

// TestEditFileTool_Issue4_NoTrailingWhitespace guards the collateral damage
// seen in the bug report: padding must not leave trailing whitespace behind on
// blank lines or shrink content that was already indented deeper than the
// target.
func TestEditFileTool_Issue4_NoTrailingWhitespace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	content := "- list item\n" +
		"    deeply indented\n" +
		"\n" +
		"- tail\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	// Blank line inside new_content must stay empty, and the deeper-indented
	// first line must not be de-indented (old preserve would strip it).
	_, err := tool.Execute(`{"path": "` + filePath + `", "operation": "insert_after", "start_line": 2, ` +
		`"new_content": "\n  spacer paragraph\n"}`)
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	want := "- list item\n" +
		"    deeply indented\n" +
		"\n" +
		"  spacer paragraph\n" +
		"\n" +
		"- tail\n"
	got := readIndentTestFile(t, filePath)
	if got != want {
		t.Errorf("file content mismatch\n got %q\nwant %q", got, want)
	}
	for i, line := range strings.Split(got, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

// TestEditFileTool_IndentMode_ExplicitOptIn proves the explicit modes are
// unchanged: `preserve` still pads to the target indent (back-compat for
// callers who passed the mode, or relied on the old default), `normalize`
// still renormalizes.
func TestEditFileTool_IndentMode_ExplicitOptIn(t *testing.T) {
	cases := []indentCase{
		{
			name: "explicit preserve still pads insert_after content",
			content: "1. First item\n" +
				"   Continuation line\n" +
				"2. Second item\n",
			payload: `"operation": "insert_after", "start_line": 2, "new_content": "padded paragraph", "indent_mode": "preserve"`,
			want: "1. First item\n" +
				"   Continuation line\n" +
				"   padded paragraph\n" +
				"2. Second item\n",
		},
		{
			name: "explicit preserve still pads replace_lines content",
			content: "- **Sandboxing**\n" +
				"  - child bullet\n" +
				"  - **Connectivity**\n" +
				"- tail\n",
			payload: `"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "- **Connectivity (network)**", "indent_mode": "preserve"`,
			want: "- **Sandboxing**\n" +
				"  - child bullet\n" +
				"  - **Connectivity (network)**\n" +
				"- tail\n",
		},
		{
			name: "explicit normalize renormalizes to the target indent",
			content: "body\n" +
				"    indented target\n",
			payload: `"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "normalized", "indent_mode": "normalize"`,
			want: "body\n" +
				"    normalized\n",
		},
		{
			name: "explicit as-is still wins when it equals the default",
			content: "1. First item\n" +
				"   Continuation line\n" +
				"2. Second item\n",
			payload: `"operation": "insert_after", "start_line": 2, "new_content": "column zero", "indent_mode": "as-is"`,
			want: "1. First item\n" +
				"   Continuation line\n" +
				"column zero\n" +
				"2. Second item\n",
		},
	}
	runIndentCases(t, cases)
}

// TestEditFileTool_Issue4_BatchDefaultAsIs covers the second call site
// (`applySingleEdit`): a batch element without indent_mode must also write
// caller content byte-for-byte.
func TestEditFileTool_Issue4_BatchDefaultAsIs(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	content := "1. Step one\n" +
		"   detail\n" +
		"2. Step two\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	// The line-addressed edit is count-stable (1:1) and comes first so the batch
	// stays legal under the stale-line-number guard; the insert re-anchors on its
	// own start_line, which the 1:1 edit left untouched.
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [` +
		`{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "1. Step one (edited)"},` +
		`{"operation": "insert_after", "start_line": 2, "new_content": "- zero column child"}` +
		`]}`)
	if err != nil {
		t.Fatalf("batch edit failed: %v", err)
	}
	want := "1. Step one (edited)\n" +
		"   detail\n" +
		"- zero column child\n" +
		"2. Step two\n"
	if got := readIndentTestFile(t, filePath); got != want {
		t.Errorf("batch file content mismatch\n got %q\nwant %q", got, want)
	}
}

// TestEditFileTool_Issue4_BatchExplicitPreserve proves the batch path still
// honors an explicit per-entry mode (back-compat).
func TestEditFileTool_Issue4_BatchExplicitPreserve(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	content := "1. Step one\n" +
		"   detail\n" +
		"2. Step two\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [` +
		`{"operation": "insert_after", "start_line": 2, "new_content": "padded", "indent_mode": "preserve"}` +
		`]}`)
	if err != nil {
		t.Fatalf("batch edit failed: %v", err)
	}
	want := "1. Step one\n" +
		"   detail\n" +
		"   padded\n" +
		"2. Step two\n"
	if got := readIndentTestFile(t, filePath); got != want {
		t.Errorf("batch file content mismatch\n got %q\nwant %q", got, want)
	}
}

// TestEditFileTool_ReplacePattern_BlockPathUnchanged pins that the multi-line
// `replace_pattern` block path — the one whose default stays `preserve` — is
// untouched by F4. The block path goes through the fuzzy matcher, which applies
// its own reindentation; `indent_mode` is not consulted there, so neither the
// new default nor an explicit mode changes the bytes it writes.
func TestEditFileTool_ReplacePattern_BlockPathUnchanged(t *testing.T) {
	content := "intro\n" +
		"    if ready:\n" +
		"        go()\n" +
		"outro\n"
	pattern := "    if ready:\n        go()"
	newContent := "    if ready:\n        go_fast()"
	want := "intro\n" +
		"    if ready:\n" +
		"        go_fast()\n" +
		"outro\n"

	// Default mode and an explicit mode must all produce identical bytes: the
	// block path never consults indent_mode, so F4's default flip cannot reach it.
	for _, mode := range []string{"", "as-is", "preserve"} {
		name := mode
		if name == "" {
			name = "(default)"
		}
		t.Run(name, func(t *testing.T) {
			runBlockPatternCase(t, content, pattern, newContent, want, mode)
		})
	}
}

func runBlockPatternCase(t *testing.T, content, pattern, newContent, want, mode string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"path":        filePath,
		"operation":   "replace_pattern",
		"pattern":     pattern,
		"new_content": newContent,
	}
	if mode != "" {
		input["indent_mode"] = mode
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	if _, err := tool.Execute(string(raw)); err != nil {
		t.Fatalf("block pattern edit failed: %v", err)
	}
	if got := readIndentTestFile(t, filePath); got != want {
		t.Errorf("file content mismatch\n got %q\nwant %q", got, want)
	}
}
