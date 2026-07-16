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
