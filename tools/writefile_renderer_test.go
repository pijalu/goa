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

func TestWriteFileRenderer_RenderCall(t *testing.T) {
	r := NewWriteFileRenderer()
	call := r.RenderCall(map[string]any{"path": "main.go"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "write main.go") {
		t.Errorf("expected 'write main.go', got %q", call)
	}
}

func TestWriteFileRenderer_RenderResult(t *testing.T) {
	r := NewWriteFileRenderer()
	out := "[write: main.go]\n✓ Written — 20 bytes, 2 lines\n```\npackage main\n\nfunc main() {}\n```\n"
	result := r.RenderResult(out, tuirender.RenderContext{Expanded: true})
	if !strings.Contains(ansi.Strip(result), "package main") {
		t.Errorf("expected content, got %q", result)
	}
	if strings.Contains(ansi.Strip(result), "✓ Written") {
		t.Errorf("result should not include status line, got %q", result)
	}
}

// TestWriteFileRenderer_RenderResult_NonFencedOutputShown verifies that when
// the output is not a fenced code block (e.g. the "(interrupted)" sentinel
// set when a write is cancelled mid-run, or an error string), the renderer
// surfaces it verbatim instead of producing an empty body. Previously such
// output was silently dropped, leaving the user with a ✗ icon and no text.
func TestWriteFileRenderer_RenderResult_NonFencedOutputShown(t *testing.T) {
	r := NewWriteFileRenderer()

	for _, out := range []string{"(interrupted)", "Error: disk full"} {
		result := r.RenderResult(out, tuirender.RenderContext{})
		stripped := ansi.Strip(result)
		if !strings.Contains(stripped, strings.TrimRight(out, "\n")) {
			t.Errorf("expected non-fenced output %q to be shown verbatim, got %q", out, stripped)
		}
	}

	// Empty output (mid-stream, before any result) must stay empty.
	if got := r.RenderResult("", tuirender.RenderContext{}); got != "" {
		t.Errorf("empty output should render empty body, got %q", got)
	}
}

func TestWriteFileRenderer_RenderResult_PreviewLimit(t *testing.T) {
	r := NewWriteFileRenderer()
	var content []string
	for i := 1; i <= writeFilePreviewLines+10; i++ {
		content = append(content, "line")
	}
	out := "[write: big.txt]\n✓ Written\n```\n" + strings.Join(content, "\n") + "\n```\n"
	result := r.RenderResult(out, tuirender.RenderContext{Expanded: false})
	if strings.Count(result, "\n") >= len(content) {
		t.Errorf("expected preview truncation, got %d lines", strings.Count(result, "\n"))
	}
	if !strings.Contains(ansi.Strip(result), "to expand") {
		t.Errorf("expected expand hint, got %q", ansi.Strip(result))
	}
}

// TestWriteFileRenderer_CompletedWriteShowsTotalLines reproduces the bugs.md
// "write stats are incorrect" item: buildWritePreview embeds only the first
// 10 content lines in the result's fenced block. After completion the widget
// must still report the TOTAL lines written — taken from the retained tool
// args — not the preview's line count.
func TestWriteFileRenderer_CompletedWriteShowsTotalLines(t *testing.T) {
	r := NewWriteFileRenderer()

	// 25-line file: the result carries the 10-line preview fence exactly as
	// buildWritePreview produces it.
	var all []string
	for i := 1; i <= 25; i++ {
		all = append(all, "line")
	}
	full := strings.Join(all, "\n")
	preview := strings.Join(all[:10], "\n")
	out := "[write: big.txt]\n✓ Written — 100 bytes, 25 lines\n```\n" + preview + "\n```\n… 15 more lines (ctrl+o to expand)\n"

	ctx := tuirender.RenderContext{
		IsPartial:    false,
		ArgsComplete: true,
		Args:         map[string]any{"path": "big.txt", "content": full},
	}
	got := ansi.Strip(r.RenderResult(out, ctx))
	if !strings.Contains(got, "25 lines") {
		t.Errorf("completed write must show total line count (25), got:\n%s", got)
	}
	if strings.Contains(got, "10 lines") {
		t.Errorf("must not report the preview's line count, got:\n%s", got)
	}

	// Fallback: without retained args (restored session), the fenced preview
	// is all there is — rendering must not break.
	got = ansi.Strip(r.RenderResult(out, tuirender.RenderContext{}))
	if !strings.Contains(got, "line") {
		t.Errorf("fenced fallback should still render content, got:\n%s", got)
	}
}

func TestWriteFileRenderer_RenderCall_StreamingShowsPath(t *testing.T) {
	r := NewWriteFileRenderer()
	call := r.RenderCall(map[string]any{"path": "main.go"}, tuirender.RenderContext{ArgsComplete: false})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "write main.go") {
		t.Errorf("expected tool name and path, got %q", stripped)
	}
	if strings.Contains(stripped, "...") {
		t.Errorf("did not expect a placeholder now that body renders the streamed content, got %q", stripped)
	}
}

func TestWriteFileRenderer_RenderResult_StreamingPreviewLimit(t *testing.T) {
	r := NewWriteFileRenderer()
	var content []string
	for i := 1; i <= writeFilePreviewLines+10; i++ {
		content = append(content, fmt.Sprintf("line %d", i))
	}
	args := map[string]any{"path": "big.go", "content": strings.Join(content, "\n")}
	result := r.RenderResult("", tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: args})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "line 1") {
		t.Errorf("expected first line in preview, got %q", stripped)
	}
	if !strings.Contains(stripped, "line 5") {
		t.Errorf("expected fifth line in preview, got %q", stripped)
	}
	if strings.Contains(stripped, "line 6") {
		t.Errorf("did not expect line 6 in collapsed preview, got %q", stripped)
	}
	if !strings.Contains(stripped, "to expand") {
		t.Errorf("expected expand hint, got %q", stripped)
	}
}

func TestWriteFileRenderer_RenderResult_StreamingShowsPartialContent(t *testing.T) {
	r := NewWriteFileRenderer()
	args := map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}"}
	result := r.RenderResult("", tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: args})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "package main") {
		t.Errorf("expected partial content in body, got %q", stripped)
	}
	if !strings.Contains(stripped, "println") {
		t.Errorf("expected streamed content, got %q", stripped)
	}
}

// TestWriteFileRenderer_WriteStatsDuringPreparation pins the collapsed
// write-preparation footer: it must always show the line-count stat and the
// Ctrl+O expand affordance — even when the whole content fits inside the
// preview window (previously such writes rendered bare content with no stats
// at all, leaving only the elapsed timer). While streaming it reads
// "writing N lines"; after completion it reports the final "N lines".
func TestWriteFileRenderer_WriteStatsDuringPreparation(t *testing.T) {
	r := NewWriteFileRenderer()
	cases := []struct {
		name    string
		render  func() string
		want    []string // substrings that must appear
		notWant []string // substrings that must not appear
	}{
		{
			name: "streaming content fits preview",
			render: func() string {
				args := map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}"}
				return r.RenderPartial(args, tuirender.RenderContext{IsPartial: true, Args: args, PreviewLines: 10})
			},
			want: []string{"writing 3 lines", "ctrl+o"},
		},
		{
			name: "streaming content exceeds preview",
			render: func() string {
				args := map[string]any{"path": "big.go", "content": strings.Repeat("line\n", 19) + "line"}
				return r.RenderPartial(args, tuirender.RenderContext{IsPartial: true, Args: args, PreviewLines: 10})
			},
			want: []string{"writing 20 lines", "10 more lines", "ctrl+o"},
		},
		{
			name: "single line uses singular",
			render: func() string {
				args := map[string]any{"path": "one.go", "content": "package main"}
				return r.RenderPartial(args, tuirender.RenderContext{IsPartial: true, Args: args, PreviewLines: 10})
			},
			want:    []string{"writing 1 line"},
			notWant: []string{"1 lines"},
		},
		{
			name: "completed result shows final count",
			render: func() string {
				out := "[write: main.go]\n✓ Written — 30 bytes, 3 lines\n```\npackage main\n\nfunc main() {}\n```\n"
				return r.RenderResult(out, tuirender.RenderContext{PreviewLines: 10})
			},
			want:    []string{"3 lines"},
			notWant: []string{"writing"},
		},
		{
			name: "expanded shows full content without footer",
			render: func() string {
				args := map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}"}
				return r.RenderPartial(args, tuirender.RenderContext{IsPartial: true, Args: args, PreviewLines: 10, Expanded: true})
			},
			notWant: []string{"writing", "ctrl+o"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stripped := ansi.Strip(tc.render())
			for _, w := range tc.want {
				if !strings.Contains(stripped, w) {
					t.Errorf("expected %q in output, got %q", w, stripped)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(stripped, nw) {
					t.Errorf("did not expect %q in output, got %q", nw, stripped)
				}
			}
		})
	}
}
