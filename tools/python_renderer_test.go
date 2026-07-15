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
	if strings.Contains(stripped, "print") {
		t.Errorf("header should not contain code; got %q", out)
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

func TestPythonRenderer_RenderResult_ShowsScriptWithPrompts(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 5\nb = 10\nprint(a + b)\n"
	out := r.RenderResult("", tuirender.RenderContext{Args: map[string]any{"code": code}})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, ">>> a = 5") {
		t.Errorf("expected first script line with >>> prompt, got %q", stripped)
	}
	if !strings.Contains(stripped, "... b = 10") {
		t.Errorf("expected continuation prompt, got %q", stripped)
	}
}

func TestPythonRenderer_RenderResult_ShowsScriptAndOutput(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 5\nb = 10\nprint(a + b)\n"
	output := "The first number is: 5\nThe second number is: 10\nThe sum is: 15"
	out := r.RenderResult(output, tuirender.RenderContext{Args: map[string]any{"code": code}})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, ">>> a = 5") {
		t.Errorf("expected script visible, got %q", stripped)
	}
	if !strings.Contains(stripped, "The sum is: 15") {
		t.Errorf("expected output visible, got %q", stripped)
	}
}

func TestPythonRenderer_RenderResult_ExpandHintWhenTruncated(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 1\nb = 2\nc = 3\nd = 4\ne = 5\nf = 6\n"
	output := "line1\nline2\nline3\nline4\nline5\nline6"
	// Summary view with a 5-line preview forces truncation of the 6-line body.
	out := r.RenderResult(output, tuirender.RenderContext{Args: map[string]any{"code": code}, PreviewLines: 5})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "more line") {
		t.Errorf("expected truncation hint, got %q", stripped)
	}
	if !strings.Contains(stripped, "line6") {
		t.Errorf("expected last output line, got %q", stripped)
	}
	if !strings.Contains(stripped, "(2 more line(s)") {
		t.Errorf("expected 2 hidden lines in hint, got %q", stripped)
	}
}

func TestPythonRenderer_RenderResult_ExpandedShowsFullScript(t *testing.T) {
	r := NewPythonRenderer()
	code := "a = 1\nb = 2\nc = 3\nd = 4\ne = 5\nf = 6\n"
	out := r.RenderResult("", tuirender.RenderContext{Expanded: true, Args: map[string]any{"code": code}})
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "f = 6") {
		t.Errorf("expected full script when expanded, got %q", stripped)
	}
	if strings.Contains(stripped, "more line") {
		t.Errorf("expanded view should not have truncation hint, got %q", stripped)
	}
}

func TestPythonRenderer_PreviewLines(t *testing.T) {
	r := NewPythonRenderer()
	if r.PreviewLines() != 12 {
		t.Errorf("PreviewLines = %d, want 12", r.PreviewLines())
	}
}

func TestPythonRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := NewPythonRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}

func TestPythonRenderer_SyntaxHighlight(t *testing.T) {
	r := NewPythonRenderer()
	code := "def add(a, b):\n    return a + b\n"
	out := r.RenderResult("", tuirender.RenderContext{Args: map[string]any{"code": code}})
	// ANSI color codes should be present on the highlighted keyword.
	if !strings.Contains(out, ansi.Fg("#d29922")) {
		t.Errorf("expected syntax highlighting color codes, got %q", out)
	}
}
