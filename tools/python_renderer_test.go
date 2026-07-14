// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

func TestPythonRenderer_RenderCall(t *testing.T) {
	r := NewPythonRenderer()
	out := r.RenderCall(map[string]any{"code": "print(1+2)"}, tuirender.RenderContext{})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "python") {
		t.Errorf("RenderCall = %q, want python label", out)
	}
	if !strings.Contains(stripped, "print(1+2)") {
		t.Errorf("RenderCall = %q, want print(1+2)", out)
	}
}

func TestPythonRenderer_RenderCall_MultilineShowsLineCount(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 1\nb = 2\nprint(a + b)\n"
	out := r.RenderCall(map[string]any{"code": code}, tuirender.RenderContext{})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "python") {
		t.Errorf("RenderCall = %q, want python label", out)
	}
	if !strings.Contains(stripped, "(3 lines)") {
		t.Errorf("RenderCall = %q, want (3 lines)", out)
	}
}

func TestPythonRenderer_RenderCall_Empty(t *testing.T) {
	r := NewPythonRenderer()
	out := r.RenderCall(map[string]any{}, tuirender.RenderContext{})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "python") {
		t.Errorf("RenderCall = %q, want python fallback", out)
	}
}

func TestPythonRenderer_RenderResult_PartialShowsCode(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 1\nb = 2\nc = 3\nd = 4\ne = 5\nprint(a + b + c + d + e)\n"
	out := r.RenderResult("", tuirender.RenderContext{IsPartial: true, Args: map[string]any{"code": code}})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "a = 1") {
		t.Errorf("RenderResult = %q, want a = 1", out)
	}
	if !strings.Contains(stripped, "more lines") {
		t.Errorf("RenderResult = %q, want line-count hint", out)
	}
}

func TestPythonRenderer_RenderResult_OutputShowsLastLines(t *testing.T) {
	r := NewPythonRenderer()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line")
	}
	out := r.RenderResult(strings.Join(lines, "\n"), tuirender.RenderContext{})
	stripped := ansi.Strip(out)
	if strings.Count(stripped, "\n") >= 6 {
		t.Errorf("expected output preview truncation, got %d lines", strings.Count(stripped, "\n"))
	}
	if !strings.Contains(stripped, "earlier lines") {
		t.Errorf("expected earlier-lines hint, got %q", out)
	}
}

func TestPythonRenderer_RenderResult_ExpandedOutput(t *testing.T) {
	r := NewPythonRenderer()
	out := r.RenderResult("a\nb\nc", tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(out)
	if strings.Count(stripped, "\n") != 2 {
		t.Errorf("expected all 3 output lines (2 newlines), got %d newlines", strings.Count(stripped, "\n"))
	}
}

func TestPythonRenderer_PreviewLines(t *testing.T) {
	r := NewPythonRenderer()
	if r.PreviewLines() != 6 {
		t.Errorf("PreviewLines = %d, want 6", r.PreviewLines())
	}
}

func TestPythonRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := NewPythonRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}
