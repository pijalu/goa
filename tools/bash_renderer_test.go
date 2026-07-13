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

func TestBashRenderer_RenderCall(t *testing.T) {
	r := NewBashRenderer()
	call := r.RenderCall(map[string]any{"command": "echo hi"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "$ echo hi") {
		t.Errorf("expected command header, got %q", call)
	}
}

func TestBashRenderer_RenderCall_CollapsesMultilineCommand(t *testing.T) {
	r := NewBashRenderer()
	cmd := "python3 -c \"\nwith open('/tmp/foo', 'r') as f:\n    print(f.read())\n\""
	call := r.RenderCall(map[string]any{"command": cmd}, tuirender.RenderContext{})
	if strings.Contains(call, "\n") {
		t.Errorf("rendered call should not contain newlines, got %q", call)
	}
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "python3 -c") {
		t.Errorf("expected collapsed command to keep content, got %q", stripped)
	}
}

func TestBashRenderer_RenderCall_TruncatesVeryLongCommand(t *testing.T) {
	r := NewBashRenderer()
	cmd := strings.Repeat("a", 200)
	call := r.RenderCall(map[string]any{"command": cmd}, tuirender.RenderContext{})
	stripped := ansi.Strip(call)
	if !strings.HasSuffix(stripped, "...") {
		t.Errorf("expected long command to be truncated with ellipsis, got %q", stripped)
	}
	if len(stripped) > 130 {
		t.Errorf("truncated command too long: %d chars", len(stripped))
	}
}

func TestBashRenderer_RenderResult_WithOutput(t *testing.T) {
	r := NewBashRenderer()
	out := "hi\nDuration: 0.05s\n"
	result := r.RenderResult(out, tuirender.RenderContext{Expanded: true, IsPartial: false})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "hi") {
		t.Errorf("expected output 'hi', got %q", result)
	}
	// The renderer must NOT emit its own timing line: the generic
	// ToolExecutionComponent duration line is the single source of truth, and
	// a second "Took" here would duplicate it (the duplicate-time bug).
	if strings.Contains(stripped, "Took") {
		t.Errorf("renderer must not emit Took line (handled by widget); got %q", stripped)
	}
	if strings.Contains(stripped, "elapsed") {
		t.Errorf("renderer must not emit elapsed line (handled by widget); got %q", stripped)
	}
	// The Duration footer must be stripped from the body, not shown raw.
	if strings.Contains(stripped, "Duration:") {
		t.Errorf("Duration footer must be stripped from body; got %q", stripped)
	}
}

func TestBashRenderer_RenderResult_PreviewLimit(t *testing.T) {
	r := NewBashRenderer()
	var lns []string
	for i := 1; i <= 10; i++ {
		lns = append(lns, "line")
	}
	out := strings.Join(lns, "\n") + "\nDuration: 0.01s\n"
	result := r.RenderResult(out, tuirender.RenderContext{Expanded: false})
	if strings.Count(result, "\n") >= 10 {
		t.Errorf("expected preview truncation, got %d lines", strings.Count(result, "\n"))
	}
	// Duration footer is stripped, never rendered by the body.
	if strings.Contains(ansi.Strip(result), "Duration:") {
		t.Errorf("Duration footer must be stripped; got %q", result)
	}
}

func TestBashRenderer_DefaultBackground(t *testing.T) {
	r := NewBashRenderer()
	if r.DefaultBackground() {
		t.Error("DefaultBackground() should return false for status-based background colors")
	}
}

func TestBashRenderer_HideWhenCollapsed(t *testing.T) {
	r := NewBashRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed() should return false")
	}
}

func TestBashRenderer_RenderResult_DurationOnly(t *testing.T) {
	r := NewBashRenderer()
	result := r.RenderResult("Duration: 0.12s\n", tuirender.RenderContext{Expanded: true, IsPartial: true})
	stripped := ansi.Strip(result)
	// A bare Duration footer yields no visible body (timing is owned by the
	// widget duration line), regardless of the partial/complete state.
	if stripped != "" {
		t.Errorf("expected empty body for duration-only output, got %q", stripped)
	}
}

func TestBashRenderer_RenderResult_WithTruncation(t *testing.T) {
	r := NewBashRenderer()
	o := strings.Repeat("line\n", 10)
	o += "Full output saved to: /tmp/foo\n"
	o += "Duration: 0.05s\n"
	result := r.RenderResult(o, tuirender.RenderContext{Expanded: false, IsPartial: false})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "[Full output: /tmp/foo]") {
		t.Errorf("expected full output notice, got %q", stripped)
	}
}

func TestBashRenderer_RenderCall_WithTimeout(t *testing.T) {
	r := NewBashRenderer()
	call := r.RenderCall(map[string]any{"command": "sleep 10", "timeout": 5}, tuirender.RenderContext{})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "sleep 10") {
		t.Errorf("expected command in call, got %q", stripped)
	}
	if !strings.Contains(stripped, "timeout 5s") {
		t.Errorf("expected timeout hint in call, got %q", stripped)
	}
}

func TestBashRenderer_RenderCall_EmptyArgs(t *testing.T) {
	r := NewBashRenderer()
	call := r.RenderCall(map[string]any{}, tuirender.RenderContext{})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "...") {
		t.Errorf("expected placeholder for empty args, got %q", stripped)
	}
}

func TestBashRenderer_parseOutput(t *testing.T) {
	r := NewBashRenderer()
	p := r.parseOutput("file.txt\nDuration: 0.12s\n")
	if !strings.Contains(p.output, "file.txt") {
		t.Errorf("output = %q, want file.txt", p.output)
	}
	if strings.Contains(p.output, "Duration:") {
		t.Errorf("Duration footer must be stripped from parsed output; got %q", p.output)
	}
}
