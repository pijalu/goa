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

func TestReadFileRenderer_RenderCall(t *testing.T) {
	r := NewReadFileRenderer()
	call := r.RenderCall(map[string]any{"path": "README.md"}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "read README.md") {
		t.Errorf("expected 'read README.md', got %q", call)
	}
}

func TestReadFileRenderer_RenderCall_FullPath(t *testing.T) {
	r := NewReadFileRenderer()
	call := r.RenderCall(map[string]any{"path": "/home/user/proj/README.md"}, tuirender.RenderContext{})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "read /home/user/proj/README.md") {
		t.Errorf("expected full path in call line, got %q", call)
	}
}

func TestReadFileRenderer_RenderCall_WithRange(t *testing.T) {
	r := NewReadFileRenderer()
	call := r.RenderCall(map[string]any{"path": "PLAN.md", "start_line": 10, "end_line": 20}, tuirender.RenderContext{})
	if !strings.Contains(ansi.Strip(call), "read PLAN.md:10-20") {
		t.Errorf("expected range header, got %q", call)
	}
}

func TestReadFileRenderer_RenderResult_EmptyForSuccess(t *testing.T) {
	r := NewReadFileRenderer()
	// Output format: "read file <path>:<start>:<end>\n<content>\n(end — N lines shown)"
	result := r.RenderResult("read file README.md:1:2\nline1\nline2\n(end — 2 lines shown)\n", tuirender.RenderContext{Expanded: false})
	if result != "" {
		t.Errorf("expected empty result for successful read, got %q", result)
	}
}

func TestReadFileRenderer_RenderResult_ShowsContentWhenExpanded(t *testing.T) {
	r := NewReadFileRenderer()
	// Collapsed (Summary) shows nothing — the path/range is on the call line.
	output := "read file main.go:1:2\n     1  package main\n     2  \n(end — 2 lines shown)\n"
	collapsed := r.RenderResult(output, tuirender.RenderContext{Expanded: false, Args: map[string]any{"path": "main.go"}})
	if collapsed != "" {
		t.Errorf("collapsed read should be empty, got %q", collapsed)
	}
	// Expanded (Full) shows the file content (pi parity).
	expanded := r.RenderResult(output, tuirender.RenderContext{Expanded: true, Args: map[string]any{"path": "main.go"}})
	if stripped := ansi.Strip(expanded); !strings.Contains(stripped, "package main") {
		t.Errorf("expanded read should show file content, got %q", expanded)
	}
}

func TestReadFileRenderer_RenderResult_TruncatedShowsHint(t *testing.T) {
	r := NewReadFileRenderer()
	output := "read file big.go:1:500\n     1  a\n(end — 50 lines shown, 450 remaining)\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true, Args: map[string]any{"path": "big.go"}})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "450 more lines") {
		t.Errorf("expanded truncated read should show 'more lines' hint, got %q", result)
	}
	if !strings.Contains(stripped, "ctrl+o") {
		t.Errorf("hint should mention the expand key, got %q", result)
	}
}

func TestReadFileRenderer_RenderResult_RemainingLinesEmpty(t *testing.T) {
	r := NewReadFileRenderer()
	// Output with remaining lines; metadata is on the call line.
	result := r.RenderResult("read file big.go:1:500\n...\n(end — 50 lines shown, 450 remaining)\n", tuirender.RenderContext{Expanded: false})
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestReadFileRenderer_RenderResult_ErrorShowsOutput(t *testing.T) {
	r := NewReadFileRenderer()
	result := r.RenderResult("Error: file not found: missing.txt", tuirender.RenderContext{IsError: true})
	if !strings.Contains(ansi.Strip(result), "Error: file not found") {
		t.Errorf("expected error text when IsError, got %q", result)
	}
}

func TestReadFileRenderer_RenderResult_EmptyOutput(t *testing.T) {
	r := NewReadFileRenderer()
	result := r.RenderResult("read file empty.txt:1:0\n(end — 0 lines shown)\n", tuirender.RenderContext{Expanded: false})
	if result != "" {
		t.Errorf("expected empty result for empty file, got %q", result)
	}
}

func TestParseTotalLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "simple end",
			output: "read file x:1:10\ncontent\n(end — 10 lines shown)\n",
			want:   "10",
		},
		{
			name:   "with remaining",
			output: "read file x:1:500\n...\n(end — 50 lines shown, 450 remaining)\n",
			want:   "50",
		},
		{
			name:   "no trailing newline",
			output: "read file x:1:5\na\nb\n(end — 5 lines shown)",
			want:   "5",
		},
		{
			name:   "no end marker",
			output: "read file x:1:5\na\nb\n",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTotalLines(tt.output)
			if got != tt.want {
				t.Errorf("parseTotalLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseOffset(t *testing.T) {
	tests := []struct {
		name     string
		rangeStr string
		want     string
	}{
		{
			name:     "simple range",
			rangeStr: "read file x:1:100",
			want:     "1-100",
		},
		{
			name:     "partial range",
			rangeStr: "read file x:10:20",
			want:     "10-20",
		},
		{
			name:     "no prefix",
			rangeStr: "something else",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOffset(tt.rangeStr)
			if got != tt.want {
				t.Errorf("parseOffset(%q) = %q, want %q", tt.rangeStr, got, tt.want)
			}
		})
	}
}
