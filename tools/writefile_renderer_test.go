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

func TestWriteFileRenderer_RenderResult_PreviewLimit(t *testing.T) {
	r := NewWriteFileRenderer()
	var content []string
	for i := 1; i <= 15; i++ {
		content = append(content, "line")
	}
	out := "[write: big.txt]\n✓ Written\n```\n" + strings.Join(content, "\n") + "\n```\n"
	result := r.RenderResult(out, tuirender.RenderContext{Expanded: false})
	if strings.Count(result, "\n") >= 15 {
		t.Errorf("expected preview truncation, got %d lines", strings.Count(result, "\n"))
	}
}
