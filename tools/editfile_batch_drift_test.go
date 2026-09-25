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

// batchDriftFixture is the Issue-2 file: five numbered lines so that every
// line is identifiable after a mis-targeted edit.
const batchDriftFixture = "one\ntwo\nthree\nfour\nfive\n"

// newBatchDriftTool writes fixture into a temp dir and returns the tool plus
// the file path.
func newBatchDriftTool(t *testing.T, fixture string) (*EditFileTool, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "drift.txt")
	if err := os.WriteFile(filePath, []byte(fixture), 0644); err != nil {
		t.Fatal(err)
	}
	return &EditFileTool{ProjectDir: dir, AllowFuzz: true}, filePath
}

// assertBatchDriftRejected runs input, requires a stale_line_numbers rejection
// with the actionable split-the-batch hint, and requires the file to be
// byte-identical to fixture (the guard must never write). It returns the error
// message for further assertions.
func assertBatchDriftRejected(t *testing.T, tool *EditFileTool, input, filePath, fixture string) string {
	t.Helper()
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("expected a stale_line_numbers rejection, got success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stale_line_numbers") {
		t.Errorf("error type must be stale_line_numbers, got: %s", msg)
	}
	if !strings.Contains(msg, "Split the batch") || !strings.Contains(msg, "re-harvest line numbers") {
		t.Errorf("error must carry the actionable two-call hint, got: %s", msg)
	}
	data, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != fixture {
		t.Errorf("rejected batch must leave the file byte-identical\n got: %q\nwant: %q", string(data), fixture)
	}
	return msg
}

// TestEditFileTool_BatchDrift_Issue2Repro is the core regression test: the
// reported batch (delete line 2, then replace line 3) used to silently
// overwrite the wrong line. On the pre-guard code it "succeeded" and wrote
// "X" over "four"; now it must be rejected with the file untouched.
func TestEditFileTool_BatchDrift_Issue2Repro(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"operation": "delete_lines", "start_line": 2, "end_line": 2},
		{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "X"}
	]}`
	msg := assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
	if !strings.Contains(msg, "edit 2/2 (replace_lines)") {
		t.Errorf("error must name the offending entry as edit 2/2 (replace_lines), got: %s", msg)
	}
	if !strings.Contains(msg, "edit 1 (delete_lines)") {
		t.Errorf("error must name the earlier line-shifting entry, got: %s", msg)
	}
}

// TestEditFileTool_BatchDrift_AbsoluteInsertThenReplaceLines covers the
// start_line-addressed insert: it is absolute too, so it invalidates later
// line numbers exactly like delete_lines does.
func TestEditFileTool_BatchDrift_AbsoluteInsertThenReplaceLines(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"operation": "insert_after", "start_line": 2, "new_content": "inserted"},
		{"operation": "replace_lines", "start_line": 4, "end_line": 4, "new_content": "X"}
	]}`
	assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
}

// TestEditFileTool_BatchDrift_ShiftingReplaceThenReplaceLines covers a content
// search/replace that removes lines: content anchors re-resolve, but the
// absolute line number after it does not.
func TestEditFileTool_BatchDrift_ShiftingReplaceThenReplaceLines(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"old_string": "two\nthree", "new_string": "TWO-THREE"},
		{"operation": "delete_lines", "start_line": 4, "end_line": 4}
	]}`
	assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
}

// TestEditFileTool_BatchDrift_MultiLinePatternThenReplaceLines covers the
// conservative replace_pattern branch: a multi-line pattern is a block edit
// and may change the line count.
func TestEditFileTool_BatchDrift_MultiLinePatternThenReplaceLines(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"operation": "replace_pattern", "pattern": "two\nthree", "new_content": "TWO"},
		{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "X"}
	]}`
	assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
}

// TestEditFileTool_BatchDrift_GrowingReplaceLinesThenReplaceLines covers a
// replace_lines that grows its range: 1 line becomes 2, so the later absolute
// coordinate is stale.
func TestEditFileTool_BatchDrift_GrowingReplaceLinesThenReplaceLines(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "one\none-and-a-half"},
		{"operation": "replace_lines", "start_line": 4, "end_line": 4, "new_content": "X"}
	]}`
	assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
}

// TestEditFileTool_BatchDrift_OpenEndedRangeRejected pins the end_line <= 0
// case ("to end of file"): the range it consumes is only known against the
// current content, so it is never line-count stable.
func TestEditFileTool_BatchDrift_OpenEndedRangeRejected(t *testing.T) {
	tool, filePath := newBatchDriftTool(t, batchDriftFixture)
	input := `{"path": "` + filePath + `", "edits": [
		{"operation": "replace_lines", "start_line": 4, "end_line": 0, "new_content": "tail"},
		{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "X"}
	]}`
	assertBatchDriftRejected(t, tool, input, filePath, batchDriftFixture)
}

// TestEditFileTool_BatchDrift_LegalReplaceLinesPairs passes batches whose
// line-numbered entries cannot go stale: every earlier entry keeps the line
// count identical (replacement length == range length).
func TestEditFileTool_BatchDrift_LegalReplaceLinesPairs(t *testing.T) {
	cases := []struct {
		name  string
		edits string
		want  string
	}{
		{
			name: "replace_lines 1:1 twice",
			edits: `{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "ONE"},` +
				`{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "THREE"}`,
			want: "ONE\ntwo\nTHREE\nfour\nfive\n",
		},
		{
			name: "multi-line range replaced by the same count",
			edits: `{"operation": "replace_lines", "start_line": 2, "end_line": 3, "new_content": "TWO\nTHREE"},` +
				`{"operation": "replace_lines", "start_line": 4, "end_line": 4, "new_content": "FOUR"}`,
			want: "one\nTWO\nTHREE\nFOUR\nfive\n",
		},
		{
			name: "explicit empty new_content keeps the line count (Issue 5)",
			edits: `{"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": ""},` +
				`{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "THREE"}`,
			want: "one\n\nTHREE\nfour\nfive\n",
		},
		{
			name: "line-numbered edit before the shifting entry",
			edits: `{"operation": "replace_lines", "start_line": 1, "end_line": 1, "new_content": "ONE"},` +
				`{"operation": "delete_lines", "start_line": 5, "end_line": 5}`,
			want: "ONE\ntwo\nthree\nfour\n",
		},
		{
			name: "single-line replace_pattern cannot shift",
			edits: `{"operation": "replace_pattern", "pattern": "^two", "new_content": "TWO"},` +
				`{"operation": "replace_lines", "start_line": 3, "end_line": 3, "new_content": "THREE"}`,
			want: "one\nTWO\nTHREE\nfour\nfive\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, filePath := newBatchDriftTool(t, batchDriftFixture)
			out, err := tool.Execute(`{"path": "` + filePath + `", "edits": [` + tc.edits + `]}`)
			if err != nil {
				t.Fatalf("legal batch must apply, got: %v", err)
			}
			if !strings.Contains(out, "edits applied") {
				t.Errorf("unexpected result: %s", out)
			}
			data, _ := os.ReadFile(filePath)
			if string(data) != tc.want {
				t.Errorf("file content = %q, want %q", string(data), tc.want)
			}
		})
	}
}

// TestEditFileTool_BatchDrift_LegalShiftThenContentOps covers the batches the
// guard must keep working: content-anchored and pattern-addressed edits
// re-anchor per edit, so they may follow line-shifting entries.
func TestEditFileTool_BatchDrift_LegalShiftThenContentOps(t *testing.T) {
	cases := []struct {
		name  string
		edits string
		want  string
	}{
		{
			name: "delete_lines then content replace",
			edits: `{"operation": "delete_lines", "start_line": 2, "end_line": 2},` +
				`{"old_string": "three", "new_string": "THREE"}`,
			want: "one\nTHREE\nfour\nfive\n",
		},
		{
			name: "growing replace then pattern-addressed insert (no start_line)",
			edits: `{"old_string": "one", "new_string": "one\none-and-a-half"},` +
				`{"operation": "insert_after", "pattern": "four", "new_content": "after-four"}`,
			want: "one\none-and-a-half\ntwo\nthree\nfour\nafter-four\nfive\n",
		},
		{
			name:  "pure content-op batch",
			edits: `{"old_string": "two", "new_string": "TWO"},{"old_string": "four", "new_string": "FOUR"}`,
			want:  "one\nTWO\nthree\nFOUR\nfive\n",
		},
		{
			name:  "single absolute edit",
			edits: `{"operation": "delete_lines", "start_line": 1, "end_line": 1}`,
			want:  "two\nthree\nfour\nfive\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, filePath := newBatchDriftTool(t, batchDriftFixture)
			if _, err := tool.Execute(`{"path": "` + filePath + `", "edits": [` + tc.edits + `]}`); err != nil {
				t.Fatalf("legal batch must apply, got: %v", err)
			}
			data, _ := os.ReadFile(filePath)
			if string(data) != tc.want {
				t.Errorf("file content = %q, want %q", string(data), tc.want)
			}
		})
	}
}

// TestUsesAbsoluteLines pins the helper's op classification.
func TestUsesAbsoluteLines(t *testing.T) {
	cases := []struct {
		name string
		e    editFileParams
		want bool
	}{
		{"replace_lines", editFileParams{Operation: "replace_lines", StartLine: 3, EndLine: 3}, true},
		{"delete_lines", editFileParams{Operation: "delete_lines", StartLine: 3, EndLine: 3}, true},
		{"insert_after by start_line", editFileParams{Operation: "insert_after", StartLine: 2}, true},
		{"insert_before by start_line", editFileParams{Operation: "insert_before", StartLine: 2}, true},
		{"insert_after by pattern", editFileParams{Operation: "insert_after", Pattern: "^x"}, false},
		{"replace alias without operation", editFileParams{OldString: "a", NewString: "b"}, false},
		{"replace_pattern", editFileParams{Operation: "replace_pattern", Pattern: "^x"}, false},
		{"missing operation", editFileParams{StartLine: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesAbsoluteLines(tc.e); got != tc.want {
				t.Errorf("usesAbsoluteLines(%+v) = %v, want %v", tc.e, got, tc.want)
			}
		})
	}
}

// TestMayShiftLines pins the line-count-change classification, including the
// cases that must NOT trigger the guard so their own error survives.
func TestMayShiftLines(t *testing.T) {
	cases := []struct {
		name string
		e    editFileParams
		want bool
	}{
		{"replace_lines 1:1", editFileParams{Operation: "replace_lines", StartLine: 1, EndLine: 1, NewContent: "x"}, false},
		{"replace_lines 2:2 same count", editFileParams{Operation: "replace_lines", StartLine: 2, EndLine: 3, NewContent: "a\nb"}, false},
		{"replace_lines explicit empty keeps 1:1", editFileParams{Operation: "replace_lines", StartLine: 2, EndLine: 2, NewContent: "", NewContentSet: true}, false},
		{"replace_lines new_string fallback", editFileParams{Operation: "replace_lines", StartLine: 1, EndLine: 1, NewString: "x"}, false},
		{"replace_lines grows", editFileParams{Operation: "replace_lines", StartLine: 1, EndLine: 1, NewContent: "a\nb"}, true},
		{"replace_lines shrinks", editFileParams{Operation: "replace_lines", StartLine: 1, EndLine: 3, NewContent: "a"}, true},
		{"replace_lines open-ended", editFileParams{Operation: "replace_lines", StartLine: 4, NewContent: "a"}, true},
		{"replace_lines invalid range", editFileParams{Operation: "replace_lines", StartLine: 0, EndLine: 3, NewContent: "a"}, false},
		{"replace_lines missing content", editFileParams{Operation: "replace_lines", StartLine: 1, EndLine: 1}, false},
		{"delete_lines", editFileParams{Operation: "delete_lines", StartLine: 1, EndLine: 1}, true},
		{"insert_after by pattern", editFileParams{Operation: "insert_after", Pattern: "^x", NewContent: "y"}, true},
		{"insert_before by line", editFileParams{Operation: "insert_before", StartLine: 1, NewContent: "y"}, true},
		{"replace same line count", editFileParams{OldString: "a\nb", NewString: "c\nd"}, false},
		{"replace trailing newline is not a line", editFileParams{OldString: "a\nb", NewString: "c\nd\n"}, false},
		{"replace drops lines", editFileParams{OldString: "a\nb", NewString: "c"}, true},
		{"replace adds lines", editFileParams{OldString: "a", NewString: "b\nc"}, true},
		{"replace_pattern single-line", editFileParams{Operation: "replace_pattern", Pattern: "^a", NewContent: "b"}, false},
		{"replace_pattern with newline pattern", editFileParams{Operation: "replace_pattern", Pattern: "a\nb", NewContent: "c"}, true},
		{"replace_pattern with escaped newline pattern", editFileParams{Operation: "replace_pattern", Pattern: `a\nb`, NewContent: "c"}, true},
		{"replace_pattern with multi-line content", editFileParams{Operation: "replace_pattern", Pattern: "^a", NewContent: "b\nc"}, true},
		{"unknown operation", editFileParams{Operation: "explode"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayShiftLines(tc.e); got != tc.want {
				t.Errorf("mayShiftLines(%+v) = %v, want %v", tc.e, got, tc.want)
			}
		})
	}
}

// TestCheckBatchLineDrift_NoRejectWhenOrderIsSafe checks the guard directly on
// the ordering contract: the shifting entry must come FIRST for a rejection.
func TestCheckBatchLineDrift_NoRejectWhenOrderIsSafe(t *testing.T) {
	absolute := editFileParams{Operation: "delete_lines", StartLine: 2, EndLine: 2}
	shifting := editFileParams{Operation: "insert_after", Pattern: "^x", NewContent: "y"}

	if err := checkBatchLineDrift([]editFileParams{absolute}); err != nil {
		t.Errorf("single absolute edit must pass, got: %v", err)
	}
	if err := checkBatchLineDrift([]editFileParams{absolute, shifting}); err != nil {
		t.Errorf("pattern-addressed insert after an absolute edit must pass, got: %v", err)
	}
	if err := checkBatchLineDrift([]editFileParams{shifting, absolute}); err == nil {
		t.Error("pattern-addressed insert before an absolute edit must be rejected")
	}
	if err := checkBatchLineDrift([]editFileParams{shifting, shifting, absolute}); err == nil {
		t.Error("absolute edit after two shifting edits must be rejected")
	}
	if err := checkBatchLineDrift(nil); err != nil {
		t.Errorf("empty batch must pass, got: %v", err)
	}
}
