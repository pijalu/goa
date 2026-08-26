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

// This file pins the all-or-nothing contract for content-targeting edit
// operations (bugs.md 2026-08-26): an edit whose replacement payload is empty
// or missing must fail up-front with missing_parameter and leave the target
// file byte-for-byte unchanged.
//
// Previously two code paths turned a lost/absent replacement payload into a
// SILENT deletion of the targeted block while reporting success:
//   - classic search/replace accepted "new_string": "" for any matched block;
//     when the replacement payload was lost upstream the whole anchored block
//     vanished from the file (reproduced live on bugs.md: old block deleted,
//     replacement never inserted, tool reported success);
//   - replace_pattern required no content at all, so every matched line was
//     replaced by an empty insertion ("0 lines affected" while deleting).
// Deliberate deletion belongs to delete_lines, never to an empty replace.

const allOrNothingFixture = "package main\n\nfunc alpha() {}\nfunc beta() {}\n"

// editAllOrNothingTool writes fixture to a temp file and returns the tool and
// file path.
func editAllOrNothingTool(t *testing.T) (*EditFileTool, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(filePath, []byte(allOrNothingFixture), 0644); err != nil {
		t.Fatal(err)
	}
	return &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}, filePath
}

// assertFileUntouched fails the test when the target file no longer matches
// the original fixture bytes.
func assertFileUntouched(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot re-read %s: %v", path, err)
	}
	if string(data) != allOrNothingFixture {
		t.Fatalf("file MUST be unchanged after the rejected edit; got:\n%q", string(data))
	}
}

// TestEditFileTool_SearchReplace_EmptyNewStringRejected locks in the live
// reproduction of the partial-edit bug: classic search/replace with an empty
// new_string used to silently delete the matched block and report success.
// The tool must refuse up-front instead — deletion has a dedicated operation
// (delete_lines), so an empty replacement is always a malformed edit.
func TestEditFileTool_SearchReplace_EmptyNewStringRejected(t *testing.T) {
	tool, filePath := editAllOrNothingTool(t)

	input := `{"path": "` + filePath + `", "old_string": "func alpha() {}", "new_string": ""}`
	res, err := tool.Execute(input)
	if err == nil {
		t.Fatalf("empty new_string must be rejected, got success: %q", res)
	}
	if !strings.Contains(err.Error(), "missing_parameter") {
		t.Errorf("expected missing_parameter error, got: %v", err)
	}
	assertFileUntouched(t, filePath)
}

// TestEditFileTool_ReplacePattern_MissingContentRejected covers both
// replace_pattern shapes: single-line patterns (line-by-line matcher) and
// multi-line block patterns (fuzzy block matcher). Without any replacement
// content neither may mutate the file — they used to delete matched content
// while reporting "0 lines affected".
func TestEditFileTool_ReplacePattern_MissingContentRejected(t *testing.T) {
	cases := map[string]struct {
		name    string
		pattern string // JSON-encoded pattern value
	}{
		"single-line": {pattern: `"^func beta"`},
		"multi-line-block": {
			pattern: `"func alpha() {}\nfunc beta() {}"`, // JSON "\n" → real newline → block matcher
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, filePath := editAllOrNothingTool(t)

			input := `{"path": "` + filePath + `", "operation": "replace_pattern", "pattern": ` + tc.pattern + `}`
			res, err := tool.Execute(input)
			if err == nil {
				t.Fatalf("replace_pattern without replacement content must be rejected, got success: %q", res)
			}
			if !strings.Contains(err.Error(), "missing_parameter") {
				t.Errorf("expected missing_parameter error, got: %v", err)
			}
			assertFileUntouched(t, filePath)
		})
	}
}

// TestEditFileTool_MultiEdit_MissingReplacementKeepsFileUntouched proves the
// batch path keeps its end-to-end atomicity once the guards are in place:
// a valid first edit plus a second edit lacking replacement content must fail
// the WHOLE call and leave the file exactly as it started.
func TestEditFileTool_MultiEdit_MissingReplacementKeepsFileUntouched(t *testing.T) {
	tool, filePath := editAllOrNothingTool(t)

	input := `{"path": "` + filePath + `", "edits": [` +
		`{"old_string": "func alpha() {}", "new_string": "func gamma() {}"},` +
		`{"operation": "replace_pattern", "pattern": "^func beta"}` +
		`]}`
	res, err := tool.Execute(input)
	if err == nil {
		t.Fatalf("batch containing a content-less replace_pattern must fail, got success: %q", res)
	}
	if !strings.Contains(err.Error(), "missing_parameter") || !strings.Contains(err.Error(), "no changes were written") {
		t.Errorf("expected missing_parameter error stressing atomicity, got: %v", err)
	}
	assertFileUntouched(t, filePath)
}

// contentStillAppliesCase describes one guarded route that must keep working
// when replacement content IS present. seed optionally overrides the file
// contents (default: allOrNothingFixture).
type contentStillAppliesCase struct {
	name   string
	seed   string
	build  func(path string) string
	wantIn string
	notIn  string
}

// TestEditFileTool_ContentRequiringOpsStillApply guards against
// over-blocking: identical calls that DO carry their replacement content keep
// working through every guarded route (classic replace, fuzzy tier included,
// and both replace_pattern shapes).
func TestEditFileTool_ContentRequiringOpsStillApply(t *testing.T) {
	cases := []contentStillAppliesCase{
		{
			name: "classic replace",
			build: func(path string) string {
				return `{"path": "` + path + `", "old_string": "func alpha() {}", "new_string": "func gamma() {}"}`
			},
			wantIn: "func gamma() {}", notIn: "func alpha() {}",
		},
		{
			name: "classic replace via fuzzy whitespace matching",
			seed: "func beta() {} // anchored\nfunc beta2() {}\n",
			build: func(path string) string {
				return `{"path": "` + path + `", "old_string": "func beta()   {} // anchored", "new_string": "func delta() {} // anchored"}`
			},
			wantIn: "func delta() {} // anchored", notIn: "beta() {} // anchored",
		},
		{
			name: "replace_pattern single-line",
			build: func(path string) string {
				return `{"path": "` + path + `", "operation": "replace_pattern", "pattern": "^func beta", "new_content": "func delta() {}"}`
			},
			wantIn: "func delta() {}", notIn: "func beta() {}",
		},
		{
			name: "replace_pattern multi-line block",
			build: func(path string) string {
				return `{"path": "` + path + `", "operation": "replace_pattern", "pattern": "func alpha() {}\nfunc beta() {}", "new_content": "// rewritten\n"}`
			},
			wantIn: "// rewritten", notIn: "func beta() {}",
		},
		{
			name: "batch edit with replacements present",
			build: func(path string) string {
				return `{"path": "` + path + `", "edits": [{"old_string": "func alpha() {}", "new_string": "func gamma() {}"}, {"operation": "replace_pattern", "pattern": "^func beta", "new_content": "func delta() {}"}]}`
			},
			wantIn: "func delta() {}", notIn: "func beta() {}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runContentStillAppliesCase(t, tc)
		})
	}
}

// runContentStillAppliesCase executes one guarded-route case and asserts the
// replacement landed while stale content is gone.
func runContentStillAppliesCase(t *testing.T, tc contentStillAppliesCase) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "apply.go")
	seed := tc.seed
	if seed == "" {
		seed = allOrNothingFixture
	}
	if err := os.WriteFile(filePath, []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir, AllowFuzz: true}
	res, err := tool.Execute(tc.build(filePath))
	if err != nil {
		t.Fatalf("guarded op with replacement content must still apply, got error: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	content := string(data)
	if !strings.Contains(content, tc.wantIn) {
		t.Errorf("replacement not applied; want %q in:\n%s", tc.wantIn, content)
	}
	if strings.Contains(content, tc.notIn) {
		t.Errorf("stale content should be gone (%q):\n%s", tc.notIn, content)
	}
	if res == "" {
		t.Error("expected non-empty success result")
	}
}
