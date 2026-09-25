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

// This file pins Issue 5: an explicitly empty "new_content" ("") is a
// deliberate request to write ONE empty line and must be accepted, while an
// OMITTED new_content/new_string stays rejected by the d3f8416 deletion guard.
// Both arrive at resolveOpContent as p.NewContent == "", so the distinction
// lives in editFileParams.NewContentSet — set only by UnmarshalJSON.

const (
	emptyContentDeltaNote = "explicit empty new_content"
	emptyFixture          = "alpha\n   \ngamma\n" // line 2 is whitespace-only
)

// editEmptyTool writes fixture to a temp file and returns the tool + path.
func editEmptyTool(t *testing.T, fixture string) (*EditFileTool, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(filePath, []byte(fixture), 0644); err != nil {
		t.Fatal(err)
	}
	return &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}, filePath
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(data)
}

// TestEditFileParams_NewContentPresence locks the wire contract: only an
// explicitly present "new_content" key sets NewContentSet, and every other
// field keeps decoding exactly as before (the embedded alias must not swallow
// or rename anything).
func TestEditFileParams_NewContentPresence(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSet bool
		want    string
	}{
		{"explicit empty", `{"new_content": ""}`, true, ""},
		{"explicit value", `{"new_content": "x\ny"}`, true, "x\ny"},
		{"omitted", `{"operation": "delete_lines"}`, false, ""},
		// JSON null carries no content: treat it like an omitted field so a line
		// op fails safely instead of writing something unintended.
		{"explicit null", `{"new_content": null}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p editFileParams
			if err := json.Unmarshal([]byte(tc.input), &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if p.NewContentSet != tc.wantSet {
				t.Errorf("NewContentSet = %v, want %v", p.NewContentSet, tc.wantSet)
			}
			if p.NewContent != tc.want {
				t.Errorf("NewContent = %q, want %q", p.NewContent, tc.want)
			}
		})
	}
}

// TestEditFileParams_NewContentPresence_FlatFieldsDecoded guards the wire
// struct: embedding the alias must leave every other field intact.
func TestEditFileParams_NewContentPresence_FlatFieldsDecoded(t *testing.T) {
	input := `{
		"path": "top.txt",
		"operation": "replace_lines",
		"old_string": "old",
		"new_string": "new",
		"start_line": 4,
		"end_line": 6,
		"pattern": "p",
		"pattern_flags": "i",
		"occurrence": 2,
		"indent_mode": "as-is",
		"new_content": "body"
	}`
	var p editFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, c := range []struct {
		field string
		got   any
		want  any
	}{
		{"path", p.Path, "top.txt"},
		{"operation", p.Operation, "replace_lines"},
		{"old_string", p.OldString, "old"},
		{"new_string", p.NewString, "new"},
		{"start_line", p.StartLine, 4},
		{"end_line", p.EndLine, 6},
		{"pattern", p.Pattern, "p"},
		{"pattern_flags", p.PatternFlags, "i"},
		{"occurrence", p.Occurrence, 2},
		{"indent_mode", p.IndentMode, "as-is"},
		{"new_content", p.NewContent, "body"},
		{"new_content_set", p.NewContentSet, true},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

// TestEditFileParams_NewContentPresence_BatchElements guards the recursion: a
// batch element is an editFileParams, so each element tracks presence on its
// own.
func TestEditFileParams_NewContentPresence_BatchElements(t *testing.T) {
	input := `{"path": "top.txt", "edits": [
		{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": ""},
		{"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "x"},
		{"operation": "replace_lines", "start_line": 3, "end_line": 3}
	]}`
	var p editFileParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Edits) != 3 {
		t.Fatalf("Edits len = %d, want 3", len(p.Edits))
	}
	want := []struct {
		set     bool
		content string
		line    int
	}{
		{true, "", 1},
		{true, "x", 2},
		{false, "", 3},
	}
	for i, w := range want {
		e := p.Edits[i]
		if e.NewContentSet != w.set || e.NewContent != w.content || e.StartLine != w.line ||
			e.Operation != "replace_lines" || e.EndLine != w.line {
			t.Errorf("Edits[%d] = %+v, want set=%v content=%q line=%d", i, e, w.set, w.content, w.line)
		}
	}
}

// TestEditFileTool_ExplicitEmptyNewContent_ReplacesLine is the Issue-5
// reproduction: replace_lines over a whitespace-only line with
// "new_content": "" must write a truly empty line and preserve the total line
// count. Pre-fix this failed with missing_parameter because "" was
// indistinguishable from an omitted field.
func TestEditFileTool_ExplicitEmptyNewContent_ReplacesLine(t *testing.T) {
	tool, filePath := editEmptyTool(t, emptyFixture)

	res, err := tool.Execute(`{"path": "` + filePath + `", "operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": ""}`)
	if err != nil {
		t.Fatalf("explicitly empty new_content must be accepted, got: %v", err)
	}
	want := "alpha\n\ngamma\n"
	if got := readFileT(t, filePath); got != want {
		t.Errorf("file = %q, want %q (one empty line, same line count)", got, want)
	}
	if !strings.Contains(res, emptyContentDeltaNote) {
		t.Errorf("result should note the explicit empty content, got: %q", res)
	}
	if !strings.Contains(res, "replaced 1 lines with 1") {
		t.Errorf("result should report 1 line replaced with 1, got: %q", res)
	}
	if n := strings.Count(readFileT(t, filePath), "\n"); n != 3 {
		t.Errorf("line count changed: got %d newlines, want 3", n)
	}
}

// TestEditFileTool_ExplicitEmptyNewContent_InsertOps verifies insert_after and
// insert_before accept an explicitly empty payload as one empty line.
func TestEditFileTool_ExplicitEmptyNewContent_InsertOps(t *testing.T) {
	cases := []struct {
		op   string
		line int
		want string
	}{
		{"insert_after", 1, "alpha\n\n   \ngamma\n"},
		{"insert_before", 1, "\nalpha\n   \ngamma\n"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			tool, filePath := editEmptyTool(t, emptyFixture)
			_, err := tool.Execute(`{"path": "` + filePath + `", "operation": "` + tc.op +
				`", "start_line": 1, "new_content": ""}`)
			if err != nil {
				t.Fatalf("%s with explicit empty new_content must be accepted, got: %v", tc.op, err)
			}
			if got := readFileT(t, filePath); got != tc.want {
				t.Errorf("file = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEditFileTool_ExplicitEmptyNewContent_ReplacePattern verifies the
// fragment semantics of F6 compose with an explicit empty payload: the matched
// text is removed and the unmatched prefix/suffix survives, while an OMITTED
// payload stays rejected (see the -Missing variants below).
func TestEditFileTool_ExplicitEmptyNewContent_ReplacePattern(t *testing.T) {
	tool, filePath := editEmptyTool(t, "keep OLD_frag end\n")

	_, err := tool.Execute(`{"path": "` + filePath + `", "operation": "replace_pattern", "pattern": "OLD_frag ", "new_content": ""}`)
	if err != nil {
		t.Fatalf("explicit empty replace_pattern must be accepted, got: %v", err)
	}
	if got, want := readFileT(t, filePath), "keep end\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// TestEditFileTool_ExplicitEmptyNewContent_Batch verifies presence tracking
// survives the batch decode path (each element is an editFileParams) and that
// the empty replacement lands like any other edit.
func TestEditFileTool_ExplicitEmptyNewContent_Batch(t *testing.T) {
	tool, filePath := editEmptyTool(t, emptyFixture)

	input := `{"path": "` + filePath + `", "edits": [` +
		`{"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": ""},` +
		`{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "delta"}` +
		`]}`
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("batch with an explicit empty element must succeed, got: %v", err)
	}
	if got, want := readFileT(t, filePath), "alpha\n\ndelta\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// TestEditFileTool_OmittedContent_StillRejected is the d3f8416 regression
// guard: without an explicit "new_content" key (or with an empty new_string as
// the only payload), a content-requiring op must still fail with
// missing_parameter and leave the file byte-identical.
func TestEditFileTool_OmittedContent_StillRejected(t *testing.T) {
	singles := []struct {
		name  string
		input string // edit body without the path field
	}{
		{"replace_lines", `"operation": "replace_lines", "start_line": 2, "end_line": 2`},
		{"insert_after", `"operation": "insert_after", "start_line": 2`},
		{"replace_pattern", `"operation": "replace_pattern", "pattern": "^gamma"`},
		// A line op whose only payload is an EMPTY new_string stays rejected too:
		// presence tracking covers new_content, the documented field for line ops,
		// so "" there is unambiguous while an empty new_string is not.
		{"empty_new_string", `"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_string": ""`},
	}
	for _, tc := range singles {
		t.Run(tc.name, func(t *testing.T) {
			tool, filePath := editEmptyTool(t, emptyFixture)
			_, err := tool.Execute(`{"path": "` + filePath + `", ` + tc.input + `}`)
			if err == nil {
				t.Fatalf("omitted content must be rejected for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "missing_parameter") {
				t.Errorf("expected missing_parameter, got: %v", err)
			}
			if got := readFileT(t, filePath); got != emptyFixture {
				t.Errorf("file must be untouched, got: %q", got)
			}
		})
	}
}

// TestEditFileTool_OmittedContent_BatchStillRejected proves the guard also
// fires for a batch element without content, and that the whole batch stays
// atomic.
func TestEditFileTool_OmittedContent_BatchStillRejected(t *testing.T) {
	tool, filePath := editEmptyTool(t, emptyFixture)
	input := `{"path": "` + filePath + `", "edits": [` +
		`{"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "ok"},` +
		`{"operation": "replace_lines", "start_line": 3, "end_line": 3}` +
		`]}`
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("batch element without content must fail the whole batch")
	}
	if !strings.Contains(err.Error(), "missing_parameter") || !strings.Contains(err.Error(), "no changes were written") {
		t.Errorf("expected missing_parameter stressing atomicity, got: %v", err)
	}
	if got := readFileT(t, filePath); got != emptyFixture {
		t.Errorf("file must be untouched, got: %q", got)
	}
}

// TestEditFileTool_NewStringFallbackPrecedence documents the interaction
// between the two payload fields when both are present: a non-empty
// new_string keeps its documented fallback role even when new_content is
// explicitly empty, so the fallback behavior is byte-for-byte unchanged.
func TestEditFileTool_NewStringFallbackPrecedence(t *testing.T) {
	tool, filePath := editEmptyTool(t, emptyFixture)

	res, err := tool.Execute(`{"path": "` + filePath + `", "operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "", "new_string": "beta"}`)
	if err != nil {
		t.Fatalf("new_string fallback must still apply, got: %v", err)
	}
	if !strings.Contains(res, "used new_string") {
		t.Errorf("result should note the new_string fallback, got: %q", res)
	}
	if got, want := readFileT(t, filePath), "alpha\nbeta\ngamma\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}
