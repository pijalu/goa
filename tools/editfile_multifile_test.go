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

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// writeTestFiles creates dir/files with the given contents and returns the
// directory.
func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// abs joins a test-relative name onto dir so the JSON inputs carry absolute
// paths (relative paths resolve through the WorktreeManager, which tests do
// not configure).
func abs(dir, name string) string { return filepath.Join(dir, name) }

func TestEditFileTool_MultiFile_FilesForm_AppliesToAll(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"a.go": "package a\n\nfunc oldName() {}\n",
		"b.go": "package b\n\nfunc oldName() {}\n",
	})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "oldName", "new_string": "newName"}]},`+
		`{"path": %q, "edits": [{"old_string": "oldName", "new_string": "newName"}]}]}`,
		abs(dir, "a.go"), abs(dir, "b.go"))

	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("multi-file batch should succeed: %v", err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if got := readTestFile(t, abs(dir, name)); !strings.Contains(got, "newName") {
			t.Errorf("%s not edited: %q", name, got)
		}
		if !strings.Contains(out, "[edit: "+abs(dir, name)+"]") {
			t.Errorf("result missing per-path summary for %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "[edit: 2 files] batch applied") {
		t.Errorf("result missing batch header:\n%s", out)
	}
}

func TestEditFileTool_MultiFile_PartialFailure_WritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"good.go":  "package g\n\nfunc oldName() {}\n",
		"drift.go": "package d\n\nfunc different() {}\n",
	})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "oldName", "new_string": "newName"}]},`+
		`{"path": %q, "edits": [{"old_string": "oldName", "new_string": "newName"}]}]}`,
		abs(dir, "good.go"), abs(dir, "drift.go"))

	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("a failing file must hard-fail the whole batch")
	}
	// The failing file's not_found type must surface for retry logic.
	if !strings.Contains(err.Error(), "not_found") {
		t.Errorf("error should carry the failing type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fail "+abs(dir, "drift.go")) ||
		!strings.Contains(err.Error(), "ok   "+abs(dir, "good.go")) {
		t.Errorf("error should list per-path results, got: %v", err)
	}
	// All-or-nothing: the file that would have succeeded is untouched.
	if got := readTestFile(t, abs(dir, "good.go")); strings.Contains(got, "newName") {
		t.Errorf("good.go must not be written when another file fails: %q", got)
	}
	if got := readTestFile(t, abs(dir, "drift.go")); strings.Contains(got, "newName") {
		t.Errorf("drift.go must not be written: %q", got)
	}
}

func TestEditFileTool_MultiFile_MixedMatchNoMatch_HardFails(t *testing.T) {
	dir := t.TempDir()
	// The pattern matches in a.go but not in b.go: a silent skip would edit
	// only a.go and report success.
	writeTestFiles(t, dir, map[string]string{
		"a.go": "package a\n\nx := 1\n",
		"b.go": "package b\n\ny := 2\n",
	})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "x := 1", "new_string": "x := 10"}]},`+
		`{"path": %q, "edits": [{"old_string": "x := 1", "new_string": "x := 10"}]}]}`,
		abs(dir, "a.go"), abs(dir, "b.go"))

	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("a non-matching pattern must hard-fail, not silently skip the file")
	}
	if got := readTestFile(t, abs(dir, "a.go")); strings.Contains(got, "x := 10") {
		t.Errorf("a.go must not be written when b.go fails: %q", got)
	}
}

func TestEditFileTool_MultiFile_GlobForm_AppliesToEveryMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"pkg/one.go":      "package pkg\n\nOldSymbol()\n",
		"pkg/two.go":      "package pkg\n\nOldSymbol()\n",
		"pkg/sub/keep.go": "package sub\n\nOldSymbol()\n", // outside the single-star pattern
	})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := `{"glob": "pkg/*.go", "edits": [{"old_string": "OldSymbol", "new_string": "NewSymbol"}]}`

	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("glob batch should succeed: %v", err)
	}
	for _, name := range []string{"pkg/one.go", "pkg/two.go"} {
		if got := readTestFile(t, abs(dir, name)); !strings.Contains(got, "NewSymbol") {
			t.Errorf("%s not edited: %q", name, got)
		}
	}
	// Only the two top-level matches; the subdirectory file keeps its symbol.
	if got := readTestFile(t, abs(dir, "pkg/sub/keep.go")); strings.Contains(got, "NewSymbol") {
		t.Errorf("pkg/sub/keep.go must stay untouched by the single-star glob: %q", got)
	}
	if !strings.Contains(out, "[edit: 2 files]") {
		t.Errorf("result should report 2 files:\n%s", out)
	}
}

func TestEditFileTool_MultiFile_GlobNoMatch_HardFails(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.go": "package a\n"})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := `{"glob": "nomatch/*.go", "edits": [{"old_string": "a", "new_string": "b"}]}`

	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("a zero-match glob must fail loudly, not report success")
	}
	te := toolErrOf(err)
	if te == nil || te.Type != "glob_no_match" {
		t.Errorf("want glob_no_match, got: %v", err)
	}
}

func TestEditFileTool_MultiFile_GlobMatchesOnlyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"pkg/real.go":       "package pkg\n\nold()\n",
		"pkg/sub/nested.go": "package sub\n\nold()\n",
	})
	if err := os.Mkdir(filepath.Join(dir, "pkg", "adirobjs.dir"), 0755); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := `{"glob": "pkg/*.go", "edits": [{"old_string": "old", "new_string": "new"}]}`

	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("glob batch should succeed: %v", err)
	}
	// Directories whose names end in .go (and nested files) are not targets:
	// only the regular top-level file matched.
	if !strings.Contains(out, "[edit: 1 files] batch applied") {
		t.Errorf("only the regular file should match:\n%s", out)
	}
	if got := readTestFile(t, abs(dir, "pkg/sub/nested.go")); strings.Contains(got, "new") {
		t.Errorf("nested file must stay untouched: %q", got)
	}
}

func TestEditFileTool_MultiFile_StaleLineGuard_PerFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"a.go": "l1\nl2\nl3\n",
		"b.go": "m1\nm2\n",
	})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	// b.go: delete_lines shifts lines; replace_lines with absolute numbers
	// after it is the stale-coordinate corruption the guard exists to stop.
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "l1", "new_string": "L1"}]},`+
		`{"path": %q, "edits": [{"operation": "delete_lines", "start_line": 1, "end_line": 1}, {"operation": "replace_lines", "start_line": 2, "end_line": 2, "new_content": "M2"}]}]}`,
		abs(dir, "a.go"), abs(dir, "b.go"))

	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("stale-line batch must be rejected")
	}
	if !strings.Contains(err.Error(), "stale_line_numbers") {
		t.Errorf("error should name stale_line_numbers, got: %v", err)
	}
	// Nothing written anywhere.
	if got := readTestFile(t, abs(dir, "a.go")); strings.Contains(got, "L1") {
		t.Errorf("a.go must not be written: %q", got)
	}
}

func TestEditFileTool_MultiFile_DuplicateTargets_ApplyOnce(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.go": "package a\n\nold()\n"})

	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	p := abs(dir, "a.go")
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "old", "new_string": "new"}]},`+
		`{"path": %q, "edits": [{"old_string": "old", "new_string": "new"}]}]}`, p, p)

	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("duplicate targets should dedupe: %v", err)
	}
	if !strings.Contains(out, "[edit: 1 files] batch applied") {
		t.Errorf("deduped batch should report 1 file:\n%s", out)
	}
	if !strings.Contains(out, "skip") {
		t.Errorf("dedupe should be reported, not silent:\n%s", out)
	}
}

func TestEditFileTool_MultiFile_FormConflicts_Rejected(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.go": "a\n", "b.go": "b\n"})
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	a, b := abs(dir, "a.go"), abs(dir, "b.go")

	cases := []struct{ name, input string }{
		{"path+files", fmt.Sprintf(`{"path": %q, "files": [{"path": %q, "edits": [{"old_string": "b", "new_string": "B"}]}]}`, a, b)},
		{"path+glob", fmt.Sprintf(`{"path": %q, "glob": "*.go", "edits": [{"old_string": "a", "new_string": "A"}]}`, a)},
		{"files+glob", fmt.Sprintf(`{"files": [{"path": %q, "edits": [{"old_string": "a", "new_string": "A"}]}], "glob": "*.go"}`, a)},
		{"files+edits", fmt.Sprintf(`{"files": [{"path": %q, "edits": [{"old_string": "a", "new_string": "A"}]}], "edits": [{"old_string": "b", "new_string": "B"}]}`, a)},
		{"glob without edits", `{"glob": "*.go"}`},
		{"files entry without path", `{"files": [{"edits": [{"old_string": "a", "new_string": "A"}]}]}`},
		{"files entry without edits", fmt.Sprintf(`{"files": [{"path": %q}]}`, a)},
		{"nested files", fmt.Sprintf(`{"files": [{"path": %q, "edits": [{"old_string": "a", "new_string": "A", "files": [{"path": %q, "edits": []}]}]}]}`, a, b)},
		{"nested glob", `{"glob": "*.go", "edits": [{"old_string": "a", "new_string": "A", "glob": "b.go"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(tc.input)
			if err == nil {
				t.Fatalf("%s: conflicting form must be rejected", tc.name)
			}
		})
	}
	// Nothing was written by any rejected call.
	if got := readTestFile(t, a); got != "a\n" {
		t.Errorf("a.go unexpectedly modified: %q", got)
	}
	if got := readTestFile(t, b); got != "b\n" {
		t.Errorf("b.go unexpectedly modified: %q", got)
	}
}

func TestEditFileTool_MultiFile_Access_DeclaresAllPaths(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"pkg/one.go": "package pkg\n",
		"pkg/two.go": "package pkg\n",
	})
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}

	acc := tool.Access(fmt.Sprintf(`{"files": [{"path": %q, "edits": []}, {"path": %q, "edits": []}]}`,
		abs(dir, "pkg/one.go"), abs(dir, "pkg/two.go")))
	if len(acc.WritePaths) != 2 {
		t.Errorf("files form: WritePaths = %v, want both targets", acc.WritePaths)
	}

	acc = tool.Access(`{"glob": "pkg/*.go", "edits": []}`)
	if len(acc.WritePaths) != 2 {
		t.Errorf("glob form: WritePaths = %v, want expanded matches", acc.WritePaths)
	}
}

func TestEditFileTool_MultiFile_ResultCarriesDiffs(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"a.go": "alpha\nbeta\n",
		"b.go": "one\ntwo\n",
	})
	tool := &EditFileTool{ProjectDir: dir, AllowFuzz: true}
	input := fmt.Sprintf(`{"files": [`+
		`{"path": %q, "edits": [{"old_string": "alpha", "new_string": "ALPHA"}]},`+
		`{"path": %q, "edits": [{"old_string": "one", "new_string": "ONE"}]}]}`,
		abs(dir, "a.go"), abs(dir, "b.go"))

	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("batch should succeed: %v", err)
	}
	// Two hunks: one per file.
	if strings.Count(out, "@@ ") != 2 {
		t.Errorf("result should contain two diff hunks:\n%s", out)
	}
}

func TestEditRenderer_MultiFile_RenderCallAndPartial(t *testing.T) {
	r := NewEditFileRenderer()
	ctx := tuirender.RenderContext{Cwd: "/tmp"}

	args := map[string]any{
		"files": []any{
			map[string]any{"path": "a.go", "edits": []any{}},
			map[string]any{"path": "b.go", "edits": []any{}},
		},
	}
	if got := ansi.Strip(r.RenderCall(args, ctx)); !strings.Contains(got, "edit a.go") || !strings.Contains(got, "+1 more files") {
		t.Errorf("RenderCall files form = %q", got)
	}
	if got := ansi.Strip(r.RenderPartial(args, ctx)); !strings.Contains(got, "2 files") {
		t.Errorf("RenderPartial files form = %q", got)
	}

	globArgs := map[string]any{
		"glob":  "pkg/*.go",
		"edits": []any{map[string]any{"old_string": "a"}},
	}
	if got := ansi.Strip(r.RenderCall(globArgs, ctx)); !strings.Contains(got, "glob:pkg/*.go") {
		t.Errorf("RenderCall glob form = %q", got)
	}
	if got := ansi.Strip(r.RenderPartial(globArgs, ctx)); !strings.Contains(got, "glob:pkg/*.go") || !strings.Contains(got, "1 edits") {
		t.Errorf("RenderPartial glob form = %q", got)
	}
}

func TestEditRenderer_MultiFile_RenderResult_Sections(t *testing.T) {
	r := NewEditFileRenderer()
	output := "[edit: 2 files] batch applied\n" +
		"[edit: a.go] 1 edits applied\n" +
		"@@ -1,2 +1,2 @@\n old\n-alpha\n+ALPHA\n" +
		"[edit: b.go] 1 edits applied\n" +
		"@@ -1,2 +1,2 @@\n one\n-one\n+ONE\n"
	stripped := ansi.Strip(r.RenderResult(output, tuirender.RenderContext{Expanded: true}))
	for _, want := range []string{"[edit: a.go]", "[edit: b.go]", "-2 alpha", "+2 ALPHA", "+2 ONE"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("rendered output missing %q:\n%s", want, stripped)
		}
	}
	// Single-hunk outputs keep their rendering: the summary stays muted above
	// the one hunk, no phantom second section.
	single := "[edit: main.go] 1 edits applied\n@@ -1,2 +1,2 @@\n old\n-a\n+b\n"
	got := ansi.Strip(r.RenderResult(single, tuirender.RenderContext{Expanded: true}))
	if strings.Count(got, "[edit:") != 1 {
		t.Errorf("single-hunk output must render exactly one section:\n%s", got)
	}
}
