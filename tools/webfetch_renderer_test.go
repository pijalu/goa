// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestWebFetchRendererRenderCall(t *testing.T) {
	r := NewWebFetchRenderer()
	got := r.RenderCall(map[string]any{
		"url":        "https://example.com",
		"action":     "fetch",
		"start_line": 1,
		"end_line":   50,
	}, tuirender.RenderContext{})
	if !strings.Contains(got, "https://example.com") {
		t.Errorf("missing URL in call render: %q", got)
	}
	if !strings.Contains(got, "fetch") {
		t.Errorf("missing action in call render: %q", got)
	}
}

func TestWebFetchRendererRenderResult(t *testing.T) {
	r := NewWebFetchRenderer()
	output := "webfetch https://example.com:1:3\nline1\nline2\nline3\n(end -- 3 lines shown, 0 remaining)"
	got := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	if !strings.Contains(got, "line1") {
		t.Errorf("missing content in result render: %q", got)
	}
	if !strings.Contains(got, "end -- 3 lines shown") {
		t.Errorf("missing footer in result render: %q", got)
	}
}

func TestWebFetchRendererCollapse(t *testing.T) {
	r := NewWebFetchRenderer()
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, "line")
	}
	output := "webfetch https://example.com:1:10\n" + strings.Join(lines, "\n") + "\n(end -- 10 lines shown, 0 remaining)"
	got := r.RenderResult(output, tuirender.RenderContext{})
	if got != "" {
		t.Errorf("expected no preview when collapsed, got %q", got)
	}
}

func TestWebFetchRendererExpandedShowsContent(t *testing.T) {
	r := NewWebFetchRenderer()
	output := "webfetch https://example.com:1:3\nline1\nline2\nline3\n(end -- 3 lines shown, 0 remaining)"
	got := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	if !strings.Contains(got, "line1") {
		t.Errorf("missing content in expanded result render: %q", got)
	}
	if !strings.Contains(got, "end -- 3 lines shown") {
		t.Errorf("missing footer in expanded result render: %q", got)
	}
}
