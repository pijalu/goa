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

func TestTerminalsRenderer_RenderCall_Send(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{"action": "send", "text": "ls -la"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(out), "ls -la") {
		t.Errorf("RenderCall should contain text, got: %q", out)
	}
}

func TestTerminalsRenderer_RenderCall_Send_TruncatesLongText(t *testing.T) {
	r := TerminalsRenderer{}
	long := strings.Repeat("x", 100)
	out := r.RenderCall(map[string]any{"action": "send", "text": long}, tuirender.RenderContext{})
	if strings.Contains(ansi.Strip(out), strings.Repeat("x", 80)) {
		t.Error("RenderCall should truncate long text")
	}
	if !strings.Contains(ansi.Strip(out), "...") {
		t.Errorf("RenderCall should show truncation marker, got: %q", ansi.Strip(out))
	}
}

func TestTerminalsRenderer_RenderCall_Send_EmptyText(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{"action": "send"}, tuirender.RenderContext{})
	if ansi.Strip(out) == "" {
		t.Error("RenderCall with empty text should not be empty")
	}
}

func TestTerminalsRenderer_RenderCall_Open(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{"action": "open", "type": "shell"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(out), "Open shell") {
		t.Errorf("RenderCall for open should mention type, got: %q", ansi.Strip(out))
	}
}

func TestTerminalsRenderer_RenderCall_Close(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{"action": "close", "sessionId": "main"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(out), "main") {
		t.Errorf("RenderCall for close should mention session, got: %q", ansi.Strip(out))
	}
}

func TestTerminalsRenderer_RenderCall_List(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{"action": "list"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(out), "List terminals") {
		t.Errorf("RenderCall for list should mention list, got: %q", ansi.Strip(out))
	}
}

func TestTerminalsRenderer_RenderCall_Unknown(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderCall(map[string]any{}, tuirender.RenderContext{})
	if ansi.Strip(out) == "" {
		t.Error("RenderCall with unknown action should not be empty")
	}
}

func TestTerminalsRenderer_RenderResult(t *testing.T) {
	r := TerminalsRenderer{}
	out := r.RenderResult("[terminals: read main]\nhello\n", tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(out), "hello") {
		t.Errorf("RenderResult should contain output, got: %q", out)
	}
}

func TestTerminalsRenderer_RenderResult_Empty(t *testing.T) {
	r := TerminalsRenderer{}
	if out := r.RenderResult("", tuirender.RenderContext{}); out != "" {
		t.Errorf("RenderResult with empty output should be empty, got: %q", out)
	}
}

func TestTerminalsRenderer_DefaultBackground(t *testing.T) {
	r := TerminalsRenderer{}
	if r.DefaultBackground() {
		t.Error("DefaultBackground should be false")
	}
}

func TestTerminalsRenderer_PreviewAndHide(t *testing.T) {
	r := TerminalsRenderer{}
	if r.PreviewLines() != 20 {
		t.Errorf("PreviewLines = %d, want 20", r.PreviewLines())
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}
