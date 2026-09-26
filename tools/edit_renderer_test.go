// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

func TestEditFileRenderer_RenderCall(t *testing.T) {
	r := NewEditFileRenderer()
	call := r.RenderCall(map[string]any{"path": "main.go"}, tuirender.RenderContext{Cwd: "/tmp"})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "edit main.go") {
		t.Errorf("expected 'edit main.go', got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_SingleLineDiff(t *testing.T) {
	r := NewEditFileRenderer()
	output := "[edit: main.go] search/replace applied — lines 4-4, match: exact match\n@@ -1,6 +1,6 @@\n package main\n \n func main() {\n-\tfmt.Println(\"hello\")\n+\tfmt.Println(\"world\")\n }\n "
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "-3") || !strings.Contains(stripped, "+3") {
		t.Errorf("expected numbered diff lines, got %q", stripped)
	}
	if !strings.Contains(stripped, `fmt.Println("hello")`) {
		t.Errorf("expected removed line content, got %q", stripped)
	}
	if !strings.Contains(stripped, `fmt.Println("world")`) {
		t.Errorf("expected added line content, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_MultilineDiff(t *testing.T) {
	r := NewEditFileRenderer()
	output := "@@ -1,5 +1,6 @@\n a\n b\n-c\n+c1\n+c2\n d\n e\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "-3 c") {
		t.Errorf("expected removed line, got %q", stripped)
	}
	if !strings.Contains(stripped, "+3 c1") || !strings.Contains(stripped, "+4 c2") {
		t.Errorf("expected added lines, got %q", stripped)
	}
	if !strings.Contains(stripped, " 1 a") {
		t.Errorf("expected context line, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_PreviewLimit(t *testing.T) {
	r := NewEditFileRenderer()
	var lines []string
	for i := 1; i <= editDiffPreviewLines+10; i++ {
		lines = append(lines, " line")
	}
	output := fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(lines), len(lines)) + strings.Join(lines, "\n") + "\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: false})
	if strings.Count(result, "\n") >= len(lines) {
		t.Errorf("expected preview truncation, got %d newlines", strings.Count(result, "\n"))
	}
	if !strings.Contains(ansi.Strip(result), "to expand") {
		t.Errorf("expected expand hint, got %q", ansi.Strip(result))
	}
}

func TestEditFileRenderer_RenderResult_NoDiff(t *testing.T) {
	r := NewEditFileRenderer()
	result := r.RenderResult("some plain status message", tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if stripped != "some plain status message" {
		t.Errorf("expected plain output, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_TabsExpanded(t *testing.T) {
	r := NewEditFileRenderer()
	output := "@@ -1,2 +1,2 @@\n-\told\n+\tnew\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	if strings.Contains(result, "\t") {
		t.Errorf("expected tabs expanded, got %q", result)
	}
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "   old") || !strings.Contains(stripped, "   new") {
		t.Errorf("expected tab-expanded content, got %q", stripped)
	}
}

func TestEditFileRenderer_PreviewLines(t *testing.T) {
	r := NewEditFileRenderer()
	if got := r.PreviewLines(); got != editDiffPreviewLines {
		t.Errorf("PreviewLines = %d, want %d", got, editDiffPreviewLines)
	}
}

func TestEditFileRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := NewEditFileRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}

func TestEditFileRenderer_RenderPartial_ShowsDiffstat(t *testing.T) {
	r := NewEditFileRenderer()
	args := map[string]any{
		"old_string": "line1\nline2\nline3",
		"new_string": "line1\nline2\nline3\nline4",
	}
	got := ansi.Strip(r.RenderPartial(args, tuirender.RenderContext{}))
	if !strings.Contains(got, "-3 lines") {
		t.Errorf("expected '-3 lines' in partial, got %q", got)
	}
	if !strings.Contains(got, "+4 lines") {
		t.Errorf("expected '+4 lines' in partial, got %q", got)
	}
}

func TestEditFileRenderer_RenderPartial_ShowsSingleSide(t *testing.T) {
	r := NewEditFileRenderer()

	// Only old_string (delete-only)
	got := ansi.Strip(r.RenderPartial(map[string]any{"old_string": "a\nb"}, tuirender.RenderContext{}))
	if !strings.Contains(got, "-2 lines") {
		t.Errorf("expected '-2 lines' for old_string only, got %q", got)
	}

	// Only new_string (insert-only)
	got = ansi.Strip(r.RenderPartial(map[string]any{"new_string": "x\ny\nz"}, tuirender.RenderContext{}))
	if !strings.Contains(got, "+3 lines") {
		t.Errorf("expected '+3 lines' for new_string only, got %q", got)
	}
}

func TestEditFileRenderer_RenderPartial_ShowsOperation(t *testing.T) {
	r := NewEditFileRenderer()
	args := map[string]any{
		"path":      "main.go",
		"operation": "replace_lines",
	}
	got := ansi.Strip(r.RenderPartial(args, tuirender.RenderContext{}))
	if !strings.Contains(got, "operation: replace_lines") {
		t.Errorf("expected operation name in partial, got %q", got)
	}
}

func TestEditFileRenderer_RenderPartial_EmptyArgs(t *testing.T) {
	r := NewEditFileRenderer()
	got := r.RenderPartial(map[string]any{}, tuirender.RenderContext{})
	if got != "" {
		t.Errorf("expected empty string for empty args, got %q", got)
	}
}

func TestEditFileRenderer_RenderPartial_SingleLineStrings(t *testing.T) {
	r := NewEditFileRenderer()
	args := map[string]any{
		"old_string": "hello",
		"new_string": "world",
	}
	got := ansi.Strip(r.RenderPartial(args, tuirender.RenderContext{}))
	if !strings.Contains(got, "-1 lines") {
		t.Errorf("expected '-1 lines' for single-line string, got %q", got)
	}
	if !strings.Contains(got, "+1 lines") {
		t.Errorf("expected '+1 lines' for single-line string, got %q", got)
	}
}

func TestEditRendererHelpers_UsePartialResets(t *testing.T) {
	for name, render := range map[string]func() string{
		"rDiffAdded":   func() string { return rDiffAdded("x") },
		"rDiffRemoved": func() string { return rDiffRemoved("x") },
		"rDiffContext": func() string { return rDiffContext("x") },
		"rInverse":     func() string { return rInverse("x") },
	} {
		t.Run(name, func(t *testing.T) {
			got := render()
			if strings.Contains(got, ansi.Reset) {
				t.Errorf("%s contains a full ANSI reset, which would kill an outer background color: %q", name, got)
			}
		})
	}
}

// TestEditFileRenderer_RenderCall_BatchPathFromEdits: batch calls nest the
// target path inside edits[] (models commonly omit the top-level path), and
// the call line used to fall back to "..." — the user could not see WHICH
// file was being edited (bugs.md BUG-4). The title must show the entry path
// and the queued-edit count.
func TestEditFileRenderer_RenderCall_BatchPathFromEdits(t *testing.T) {
	r := NewEditFileRenderer()
	args := map[string]any{
		"edits": []any{
			map[string]any{"path": "src/main.go", "old_string": "a", "new_string": "b"},
			map[string]any{"path": "src/main.go", "operation": "replace_lines", "start_line": 5.0, "new_content": "c"},
			map[string]any{"path": "src/main.go", "operation": "delete_lines", "start_line": 9.0, "end_line": 10.0},
		},
	}
	call := ansi.Strip(r.RenderCall(args, tuirender.RenderContext{Cwd: "/tmp"}))
	if !strings.Contains(call, "edit src/main.go") {
		t.Errorf("batch call title must name the edits[i].path file, got %q", call)
	}
	if !strings.Contains(call, "+2 more edits") {
		t.Errorf("batch call title must report the queued-edit count, got %q", call)
	}

	// Top-level path still wins when both shapes are present.
	args["path"] = "other.go"
	call = ansi.Strip(r.RenderCall(args, tuirender.RenderContext{Cwd: "/tmp"}))
	if !strings.Contains(call, "edit other.go") {
		t.Errorf("top-level path must win over edits[i].path, got %q", call)
	}

	// No path anywhere: the placeholder is back.
	call = ansi.Strip(r.RenderCall(map[string]any{}, tuirender.RenderContext{Cwd: "/tmp"}))
	if !strings.Contains(call, "edit ...") {
		t.Errorf("missing path must keep the ... placeholder, got %q", call)
	}
}

// TestEditFileRenderer_RenderPartial_BatchShowsPath: the streaming preview
// for a batch must name the file, not just the edit count.
func TestEditFileRenderer_RenderPartial_BatchShowsPath(t *testing.T) {
	r := NewEditFileRenderer()
	args := map[string]any{
		"edits": []any{
			map[string]any{"path": "cmd/app.go", "old_string": "a", "new_string": "b"},
			map[string]any{"path": "cmd/app.go", "old_string": "c", "new_string": "d"},
		},
	}
	partial := ansi.Strip(r.RenderPartial(args, tuirender.RenderContext{Cwd: "/tmp"}))
	if !strings.Contains(partial, "cmd/app.go") || !strings.Contains(partial, "2 edits") {
		t.Errorf("batch streaming preview must show path and count, got %q", partial)
	}
}

// TestEditFileRenderer_RenderResult_KeepsHeaderPath: the tool result begins
// with "[edit: <path>] N edits applied — …" above the first @@ hunk; the
// renderer used to drop everything before the hunk, so the rendered diff no
// longer named the file it edits (bugs.md BUG-4). The header must survive,
// including a fuzzy-match note that precedes it.
func TestEditFileRenderer_RenderResult_KeepsHeaderPath(t *testing.T) {
	r := NewEditFileRenderer()
	output := "[edit: internal/app/bootstrap.go] 3 edits applied — match: exact match\n" +
		"@@ -1,3 +1,3 @@\n a\n-b\n+b1\n c\n"
	result := ansi.Strip(r.RenderResult(output, tuirender.RenderContext{Expanded: true}))
	if !strings.Contains(result, "[edit: internal/app/bootstrap.go] 3 edits applied") {
		t.Errorf("rendered diff must keep the [edit: <path>] header, got %q", result)
	}
	if strings.HasPrefix(result, "@") {
		t.Errorf("header must come before the hunk lines, got %q", result)
	}

	// A fuzzy note ahead of the header survives too.
	noted := "Note: matched at 92% similarity\n" + output
	result = ansi.Strip(r.RenderResult(noted, tuirender.RenderContext{Expanded: true}))
	if !strings.Contains(result, "matched at 92% similarity") || !strings.Contains(result, "[edit: internal/app/bootstrap.go]") {
		t.Errorf("preamble lines above the hunk must be kept, got %q", result)
	}

	// Header-only output (empty hunk body) still renders the summary.
	headerOnly := "[edit: x.go] 1 edits applied\n@@ -1,0 +1,0 @@\n"
	result = ansi.Strip(r.RenderResult(headerOnly, tuirender.RenderContext{Expanded: true}))
	if !strings.Contains(result, "[edit: x.go]") {
		t.Errorf("header-only result must still name the file, got %q", result)
	}
}
