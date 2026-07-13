// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestPythonRenderer_RenderCall(t *testing.T) {
	r := PythonRenderer{}
	out := r.RenderCall(map[string]any{"code": "print(1+2)"}, tuirender.RenderContext{})
	if !strings.Contains(out, "print(1+2)") {
		t.Errorf("RenderCall = %q, want print(1+2)", out)
	}
}

func TestPythonRenderer_RenderCall_Empty(t *testing.T) {
	r := PythonRenderer{}
	out := r.RenderCall(map[string]any{}, tuirender.RenderContext{})
	if !strings.Contains(out, "python") {
		t.Errorf("RenderCall = %q, want python fallback", out)
	}
}

func TestPythonRenderer_RenderResult(t *testing.T) {
	r := PythonRenderer{}
	out := r.RenderResult("hello", tuirender.RenderContext{})
	if !strings.Contains(out, "hello") {
		t.Errorf("RenderResult = %q, want hello", out)
	}
}

func TestPythonRenderer_PreviewLines(t *testing.T) {
	r := PythonRenderer{}
	if r.PreviewLines() != 12 {
		t.Errorf("PreviewLines = %d, want 12", r.PreviewLines())
	}
}

func TestPythonRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := PythonRenderer{}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}
