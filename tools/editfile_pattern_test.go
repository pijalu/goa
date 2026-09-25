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

// This file pins the Issue-6 contract for single-line `replace_pattern`:
// `occurrence` selects the Nth matching LINE, and inside that line only the
// MATCHED TEXT is substituted. The unmatched prefix and suffix survive
// byte-for-byte.
//
// Previously a matching line was replaced wholesale, so
// "PREFIX keepme OLD_FRAGMENT suffix" became "NEW_FRAGMENT" and the model's
// surrounding text silently vanished. Two collateral defects are pinned here
// as well: `occurrence` beyond the number of matching lines used to be a
// silent no-op that still reported success, and a substitution that changes
// nothing used to be reported as a successful edit.

type patternCase struct {
	name       string
	content    string
	pattern    string
	newContent string
	flags      string
	indentMode string
	occurrence int
	want       string // exact expected file content on success
	wantErr    string // substring expected in the error message
}

func TestEditFileTool_ReplacePattern_FragmentSemantics(t *testing.T) {
	cases := []patternCase{
		{
			name:    "issue 6 repro keeps prefix and suffix",
			content: "PREFIX keepme OLD_FRAGMENT suffix\nsecond line\n",
			pattern: "OLD_FRAGMENT", newContent: "NEW_FRAGMENT",
			want: "PREFIX keepme NEW_FRAGMENT suffix\nsecond line\n",
		},
		{
			name:    "multibyte neighbours are preserved byte-for-byte",
			content: "① — § PREFIX OLD § — ①\n",
			pattern: "OLD", newContent: "NEW",
			want: "① — § PREFIX NEW § — ①\n",
		},
		{
			name:    "regex character class matches only the fragment",
			content: "value = 12345; // counted\ntail\n",
			pattern: `\d+`, newContent: "42",
			want: "value = 42; // counted\ntail\n",
		},
		{
			name:    "anchored regex substitutes the anchored fragment",
			content: "func beta() {}\nfunc gamma() {}\n",
			pattern: "^func beta", newContent: "func delta",
			want: "func delta() {}\nfunc gamma() {}\n",
		},
		{
			name:    "invalid regex falls back to literal text",
			content: "call a(b here\ntail\n",
			pattern: "a(b", newContent: "X",
			want: "call X here\ntail\n",
		},
		{
			name:    "case-insensitive match keeps surrounding text case",
			content: "Set MAX_RETRIES = TRUE;\n",
			pattern: "max_retries", newContent: "MIN", flags: "i",
			want: "Set MIN = TRUE;\n",
		},
		{
			name:    "case-insensitive alternation applies to every branch",
			content: "alpha BETA gamma\n",
			pattern: "alpha|beta", newContent: "X", flags: "i",
			want: "X X gamma\n",
		},
		{
			name:    "occurrence selects the Nth matching line only",
			content: "OLD a\nOLD b\nOLD c\n",
			pattern: "OLD", newContent: "NEW", occurrence: 2,
			want: "OLD a\nNEW b\nOLD c\n",
		},
		{
			name:    "multi-line replacement splices new lines in place",
			content: "before\nX = 1; // trailing\nafter\n",
			pattern: "X = 1", newContent: "X = 2\nY = 3",
			want: "before\nX = 2\nY = 3; // trailing\nafter\n",
		},
		{
			name:    "dollar sequences in the replacement stay literal",
			content: "prefix OLD suffix\n",
			pattern: "OLD", newContent: "$1${2}",
			want: "prefix $1${2} suffix\n",
		},
		{
			name:    "indent_mode does not re-indent the substituted fragment",
			content: "func main() {\n\tvalue := 1 // comment\n}\n",
			pattern: "value := 1", newContent: "value := 2", indentMode: "preserve",
			want: "func main() {\n\tvalue := 2 // comment\n}\n",
		},
		{
			name:    "case-sensitive default does not match other case",
			content: "Set MAX_RETRIES = TRUE;\n",
			pattern: "max_retries", newContent: "MIN",
			wantErr: "pattern_not_found",
		},
		{
			name:    "pattern not present reports pattern_not_found",
			content: "nothing to see\n",
			pattern: "OLD_FRAGMENT", newContent: "NEW",
			wantErr: "pattern_not_found",
		},
		{
			name:    "occurrence beyond match count is occurrence_not_found",
			content: "OLD a\nOLD b\nkeep\n",
			pattern: "OLD", newContent: "NEW", occurrence: 3,
			wantErr: "occurrence_not_found",
		},
		{
			name:    "identical replacement is no_change",
			content: "prefix OLD_FRAGMENT suffix\n",
			pattern: "OLD_FRAGMENT", newContent: "OLD_FRAGMENT",
			wantErr: "no_change",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPatternCase(t, tc)
		})
	}
}

// runPatternCase executes one replace_pattern case through the real tool
// entry point and asserts either the exact file content (success) or the
// error type plus a byte-for-byte untouched file (failure).
func runPatternCase(t *testing.T, tc patternCase) {
	t.Helper()
	tool, path := patternFixture(t, tc.content)
	result, execErr := tool.Execute(patternRequest(t, path, tc))
	got := readFileString(t, path)
	if tc.wantErr != "" {
		assertPatternFailure(t, tc, execErr, got)
		return
	}
	assertPatternSuccess(t, result, execErr, got, tc.want)
}

// patternRequest renders a replace_pattern call as the tool's JSON input.
func patternRequest(t *testing.T, path string, tc patternCase) string {
	t.Helper()
	req := map[string]any{
		"path":        path,
		"operation":   "replace_pattern",
		"pattern":     tc.pattern,
		"new_content": tc.newContent,
	}
	if tc.flags != "" {
		req["pattern_flags"] = tc.flags
	}
	if tc.occurrence > 0 {
		req["occurrence"] = tc.occurrence
	}
	if tc.indentMode != "" {
		req["indent_mode"] = tc.indentMode
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// assertPatternFailure requires the documented error type and proves the edit
// wrote nothing.
func assertPatternFailure(t *testing.T, tc patternCase, execErr error, got string) {
	t.Helper()
	if execErr == nil {
		t.Fatalf("expected %s error, got success with file %q", tc.wantErr, got)
	}
	if !strings.Contains(execErr.Error(), tc.wantErr) {
		t.Fatalf("expected %s error, got: %v", tc.wantErr, execErr)
	}
	if got != tc.content {
		t.Fatalf("failed edit must leave the file untouched\n got: %q\nwant: %q", got, tc.content)
	}
}

// assertPatternSuccess requires the exact resulting content and a result
// message that names the operation and its affected-line count.
func assertPatternSuccess(t *testing.T, result string, execErr error, got, want string) {
	t.Helper()
	if execErr != nil {
		t.Fatalf("unexpected error: %v", execErr)
	}
	if got != want {
		t.Fatalf("unexpected file content\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(result, "replace_pattern") {
		t.Errorf("result message should name the operation, got: %q", result)
	}
	if !strings.Contains(result, "lines affected") {
		t.Errorf("result message should report affected lines, got: %q", result)
	}
}

// TestEditFileTool_ReplacePattern_OccurrenceOverflowKeepsFile locks the
// collateral defect: occurrence > match count used to no-op while reporting
// success, leaving the caller convinced the edit landed.
func TestEditFileTool_ReplacePattern_OccurrenceOverflowKeepsFile(t *testing.T) {
	tool, path := patternFixture(t, "OLD a\nOLD b\nkeep\n")

	_, err := tool.Execute(`{"path": ` + jsonString(path) + `, "operation": "replace_pattern", "pattern": "OLD", "new_content": "NEW", "occurrence": 3}`)
	if err == nil {
		t.Fatal("expected occurrence_not_found, got success")
	}
	if !strings.Contains(err.Error(), "occurrence_not_found") {
		t.Fatalf("expected occurrence_not_found, got: %v", err)
	}
	if !strings.Contains(err.Error(), "matched 2 line(s)") {
		t.Errorf("error should report how many lines matched, got: %v", err)
	}
	assertFileContent(t, path, "OLD a\nOLD b\nkeep\n")
}

// TestEditFileTool_ReplacePattern_FragmentDiffShown ensures the emitted diff
// reflects the fragment change (the renderer keys off this diff).
func TestEditFileTool_ReplacePattern_FragmentDiffShown(t *testing.T) {
	tool, path := patternFixture(t, "PREFIX keepme OLD_FRAGMENT suffix\n")

	result, err := tool.Execute(`{"path": ` + jsonString(path) + `, "operation": "replace_pattern", "pattern": "OLD_FRAGMENT", "new_content": "NEW_FRAGMENT"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "-PREFIX keepme OLD_FRAGMENT suffix") {
		t.Errorf("diff should show the old full line, got: %q", result)
	}
	if !strings.Contains(result, "+PREFIX keepme NEW_FRAGMENT suffix") {
		t.Errorf("diff should show the substituted full line, got: %q", result)
	}
}

// TestCompileLinePattern pins the matcher used by replace_pattern.
func TestCompileLinePattern(t *testing.T) {
	cases := []struct {
		name          string
		pattern       string
		caseSensitive bool
		line          string
		want          bool
	}{
		{name: "regex digit class", pattern: `\d+`, caseSensitive: true, line: "v = 12", want: true},
		{name: "case-sensitive rejects other case", pattern: "hello", caseSensitive: true, line: "HELLO", want: false},
		{name: "case-insensitive accepts other case", pattern: "hello", caseSensitive: false, line: "HELLO", want: true},
		{name: "invalid regex falls back to literal", pattern: "a(b", caseSensitive: true, line: "call a(b", want: true},
		{name: "invalid regex literal is case-insensitive", pattern: "a(b", caseSensitive: false, line: "call A(B", want: true},
		{name: "invalid regex literal still rejects absent text", pattern: "a(b", caseSensitive: false, line: "call a-b", want: false},
		{name: "case-insensitive alternation covers every branch", pattern: "alpha|beta", caseSensitive: false, line: "xxBETA", want: true},
		{name: "case-insensitive anchor still anchored", pattern: "^func beta", caseSensitive: false, line: "  FUNC BETA", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compileLinePattern(tc.pattern, tc.caseSensitive).MatchString(tc.line); got != tc.want {
				t.Errorf("compileLinePattern(%q, %v).MatchString(%q) = %v, want %v",
					tc.pattern, tc.caseSensitive, tc.line, got, tc.want)
			}
		})
	}
}

// TestCompileLinePattern_InsensitiveKeepsInputCase proves the (?i) rewrite
// does not lowercase the line: the substituted result must keep the original
// case of the text outside the match. The previous implementation lowercased
// the input line, which mangled whatever it wrote back.
func TestCompileLinePattern_InsensitiveKeepsInputCase(t *testing.T) {
	re := compileLinePattern("world", false)
	got := re.ReplaceAllLiteralString("HELLO World", "there")
	if got != "HELLO there" {
		t.Errorf("case-insensitive substitution must preserve surrounding case, got %q", got)
	}
}

// TestMatchLine_DelegatesToCompileLinePattern keeps the shared matcher honest:
// matchLine is now a thin wrapper, so its behaviour must stay aligned.
func TestMatchLine_DelegatesToCompileLinePattern(t *testing.T) {
	if !matchLine("HELLO", "hello", false) {
		t.Error("case-insensitive matchLine should match")
	}
	if matchLine("HELLO", "hello", true) {
		t.Error("case-sensitive matchLine should not match")
	}
	if !matchLine("hello (world", "(world", true) {
		t.Error("invalid regex should fall back to a literal match")
	}
}

// patternFixture writes content to a temp file and returns a tool plus the
// file path.
func patternFixture(t *testing.T, content string) (*EditFileTool, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "pattern.txt")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return &EditFileTool{ProjectDir: dir}, filePath
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	if got := readFileString(t, path); got != want {
		t.Fatalf("unexpected file content\n got: %q\nwant: %q", got, want)
	}
}
