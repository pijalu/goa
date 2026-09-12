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

// TestAnalyzeBlockMatch covers the contiguous-block diagnostic directly:
// run must measure CONSECUTIVE overlap, anywhere the scattered per-line one.
func TestAnalyzeBlockMatch(t *testing.T) {
	tests := []struct {
		name                 string
		content, old         string
		total, run, anywhere int
		runStart             int // 0 when run == 0
	}{
		{
			name:    "non-contiguous glue of two adjacent-looking regions",
			content: "var (\n\ta int\n)\n\nfunc helper() {}\n\nfunc target() {\n\treturn\n}\n",
			old:     "var (\n\ta int\n)\n\nfunc target() {\n\treturn\n}",
			total:   6, run: 3, runStart: 1, anywhere: 6,
		},
		{
			name:    "plain drift: some lines changed",
			content: "alpha\nbeta\ngamma\ndelta\n",
			old:     "alpha\nbeta\nZZZ\nYYY",
			total:   4, run: 2, runStart: 1, anywhere: 2,
		},
		{
			name:    "no overlap at all",
			content: "alpha\nbeta\n",
			old:     "ZZZ\nYYY",
			total:   2, run: 0, runStart: 0, anywhere: 0,
		},
		{
			name:    "blank-line drift: all non-empty lines contiguous",
			content: "alpha\nbeta\ngamma\n",
			old:     "alpha\n\nbeta\n\ngamma",
			total:   3, run: 3, runStart: 1, anywhere: 3,
		},
		{
			name:    "empty old string",
			content: "alpha\n",
			old:     "\n\n",
			total:   0, run: 0, runStart: 0, anywhere: 0,
		},
		{
			name:    "crlf file and old normalized",
			content: "alpha\r\nbeta\r\ngamma\r\n",
			old:     "alpha\r\nbeta\r\n",
			total:   2, run: 2, runStart: 1, anywhere: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := analyzeBlockMatch(tc.content, tc.old)
			if m.total != tc.total || m.run != tc.run || m.anywhere != tc.anywhere || m.runStart != tc.runStart {
				t.Errorf("analyzeBlockMatch = {total:%d run:%d runStart:%d anywhere:%d}, want {total:%d run:%d runStart:%d anywhere:%d}",
					m.total, m.run, m.runStart, m.anywhere, tc.total, tc.run, tc.runStart, tc.anywhere)
			}
		})
	}
}

// TestEditNotFound_NonContiguousDiagnostic reproduces export
// goa-export-20260912-135102 ("edit drift"): the model glued the var block and
// a later function into one old_string, skipping the function in between.
// Every line existed in the file, so the old per-line diagnostic claimed
// "22/22 lines matched" and the model concluded the file matched and went
// hunting for invisible whitespace. The diagnostic must now report the
// non-contiguity instead.
func TestEditNotFound_NonContiguousDiagnostic(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "guest.go")
	content := "package actions\n\nvar (\n\tguestRateMu   sync.Mutex\n\tguestRateHits = map[string][]time.Time{}\n)\n\n// guestClientIP returns the client IP.\nfunc guestClientIP() string {\n\treturn \"host\"\n}\n\n// guestRateAllow records an attempt.\nfunc guestRateAllow(key string) bool {\n\tguestRateMu.Lock()\n\tdefer guestRateMu.Unlock()\n\treturn true\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// old_string glues the var block directly onto guestRateAllow, skipping
	// guestClientIP — every line exists in the file, just not adjacent.
	old := "var (\n\tguestRateMu   sync.Mutex\n\tguestRateHits = map[string][]time.Time{}\n)\n\n// guestRateAllow records an attempt.\nfunc guestRateAllow(key string) bool {\n\tguestRateMu.Lock()\n\tdefer guestRateMu.Unlock()\n\treturn true\n}"
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "old_string": ` + jsonQuote(old) + `, "new_string": "X"}`)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not_found") {
		t.Errorf("expected not_found, got: %s", msg)
	}
	if !strings.Contains(msg, "NOT one contiguous block") {
		t.Errorf("diagnostic must flag non-contiguity, got: %s", msg)
	}
	// The var block is 4 lines at file lines 3-6; guestRateAllow is 6 lines at
	// 13-18 — the best contiguous run is 6, NOT 10/10.
	if !strings.Contains(msg, "6/10") {
		t.Errorf("diagnostic must report the contiguous run (6/10), got: %s", msg)
	}
	// Scattered presence is reported honestly (10/10 lines exist SOMEWHERE),
	// but only as a qualifier to the non-contiguity verdict, never as a bare
	// "10/10 matched" claim.
	if !strings.Contains(msg, "scattered across non-adjacent regions") {
		t.Errorf("diagnostic must explain the scattered match, got: %s", msg)
	}
	if !strings.Contains(msg, "split old_string into one edit per region") {
		t.Errorf("diagnostic must steer to splitting the edit, got: %s", msg)
	}
}

// TestEditNotFound_ScatteredInBatchDiagnostic verifies the batch path
// (wrapMultiEditError) surfaces the same non-contiguity diagnostic.
func TestEditNotFound_ScatteredInBatchDiagnostic(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.txt")
	content := "aaa\nbbb\n\nccc\nddd\neee\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	_, err := tool.Execute(`{"path": "` + filePath + `", "edits": [
		{"old_string": "aaa\nbbb\nccc\nddd", "new_string": "X"}
	]}`)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "edit 1/1") {
		t.Errorf("error should identify the failing edit, got: %s", msg)
	}
	// bbb and ccc are not adjacent (blank line between): run is 2, anywhere 4.
	if !strings.Contains(msg, "NOT one contiguous block") || !strings.Contains(msg, "2/4") {
		t.Errorf("batch error must report non-contiguity, got: %s", msg)
	}
}

// TestEditFileTool_BatchPathInsideEntries reproduces the missing_path failures
// from export goa-export-20260912-135102: glm-5-3-flash put "path" inside each
// edits[] element and got "No 'path' provided" twice, despite providing it.
// The entry path is now a valid fallback.
func TestEditFileTool_BatchPathInsideEntries(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	out, err := tool.Execute(`{"edits": [
		{"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "TWO", "path": "` + filePath + `"},
		{"old_string": "three", "new_string": "THREE", "path": "` + filePath + `"}
	]}`)
	if err != nil {
		t.Fatalf("batch with entry paths must succeed, got: %v", err)
	}
	if !strings.Contains(out, "2 edits applied") {
		t.Errorf("unexpected result: %s", out)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "one\nTWO\nTHREE\n" {
		t.Errorf("file content = %q, want %q", string(data), "one\nTWO\nTHREE\n")
	}
}

// TestEditFileTool_BatchConflictingEntryPaths ensures a batch cannot silently
// edit a different file than an entry names.
func TestEditFileTool_BatchConflictingEntryPaths(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, f := range []string{fileA, fileB} {
		if err := os.WriteFile(f, []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tool := &EditFileTool{ProjectDir: dir}
	_, err := tool.Execute(`{"path": "` + fileA + `", "edits": [
		{"old_string": "x", "new_string": "y", "path": "` + fileB + `"}
	]}`)
	if err == nil {
		t.Fatal("expected conflicting_path error")
	}
	if !strings.Contains(err.Error(), "conflicting_path") {
		t.Errorf("expected conflicting_path, got: %v", err)
	}
	data, _ := os.ReadFile(fileA)
	if string(data) != "x\n" {
		t.Errorf("file must be untouched, got: %q", string(data))
	}
}

// TestEditFileTool_AccessFallsBackToEntryPath keeps the permission view
// (ToolAccess.WritePaths) consistent with what Execute will actually write.
func TestEditFileTool_AccessFallsBackToEntryPath(t *testing.T) {
	tool := &EditFileTool{}
	acc := tool.Access(`{"edits": [{"operation": "delete_lines", "start_line": 1, "end_line": 1, "path": "a.go"}]}`)
	if len(acc.WritePaths) != 1 || acc.WritePaths[0] != "a.go" {
		t.Errorf("WritePaths = %v, want [a.go]", acc.WritePaths)
	}
}

// jsonQuote renders s as a JSON string literal (test inputs contain tabs and
// newlines that must survive embedding into the raw JSON tool input).
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
