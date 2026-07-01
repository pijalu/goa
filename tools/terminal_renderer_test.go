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

func TestTerminalRenderer_RenderCall(t *testing.T) {
	r := TerminalRenderer{}
	call := r.RenderCall(map[string]any{"command": "echo hi"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "$ echo hi") {
		t.Errorf("expected command header, got %q", call)
	}
}

func TestTerminalRenderer_RenderCall_TruncatesLongCommand(t *testing.T) {
	r := TerminalRenderer{}
	cmd := strings.Repeat("a", 100)
	call := r.RenderCall(map[string]any{"command": cmd}, tuirender.RenderContext{})
	stripped := ansi.Strip(call)
	if !strings.HasSuffix(stripped, "...") {
		t.Errorf("expected long command to be truncated with ellipsis, got %q", stripped)
	}
}

func TestTerminalRenderer_RenderCall_EmptyCommand(t *testing.T) {
	r := TerminalRenderer{}
	call := r.RenderCall(map[string]any{}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "...") {
		t.Errorf("expected placeholder for empty command, got %q", call)
	}
}

func TestTerminalRenderer_RenderResult(t *testing.T) {
	r := TerminalRenderer{}
	result := r.RenderResult("hello\nworld", tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(result), "hello") {
		t.Errorf("expected output 'hello', got %q", result)
	}
}

func TestTerminalRenderer_RenderResult_Empty(t *testing.T) {
	r := TerminalRenderer{}
	result := r.RenderResult("", tuirender.RenderContext{})
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestTerminalRenderer_DefaultBackground(t *testing.T) {
	r := TerminalRenderer{}
	if r.DefaultBackground() {
		t.Error("DefaultBackground() should return false for status-based background colors")
	}
}

func TestTerminalRenderer_PreviewAndHide(t *testing.T) {
	r := TerminalRenderer{}
	if lines := r.PreviewLines(); lines != 20 {
		t.Errorf("PreviewLines() = %d, want 20", lines)
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed() should return false")
	}
}
