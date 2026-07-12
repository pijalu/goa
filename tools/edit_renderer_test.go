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

func TestEditFileRenderer_RenderCall(t *testing.T) {
	r := NewEditFileRenderer()
	call := r.RenderCall(map[string]any{"path": "main.go"}, tuirender.RenderContext{Cwd: "/tmp"})
	stripped := ansi.Strip(call)
	if !strings.Contains(stripped, "edit main.go") {
		t.Errorf("expected 'edit main.go', got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_SingleLineDiff(t *testing.T) {
	r := NewEditFileRenderer()
	output := "[edit: main.go] search/replace applied — lines 4-4, match: exact match\n@@ -1,6 +1,6 @@\n package main\n \n func main() {\n-\tfmt.Println(\"hello\")\n+\tfmt.Println(\"world\")\n }\n "
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "-3") || !strings.Contains(stripped, "+3") {
		t.Errorf("expected numbered diff lines, got %q", stripped)
	}
	if !strings.Contains(stripped, `fmt.Println("hello")`) {
		t.Errorf("expected removed line content, got %q", stripped)
	}
	if !strings.Contains(stripped, `fmt.Println("world")`) {
		t.Errorf("expected added line content, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_MultilineDiff(t *testing.T) {
	r := NewEditFileRenderer()
	output := "@@ -1,5 +1,6 @@\n a\n b\n-c\n+c1\n+c2\n d\n e\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "-3 c") {
		t.Errorf("expected removed line, got %q", stripped)
	}
	if !strings.Contains(stripped, "+3 c1") || !strings.Contains(stripped, "+4 c2") {
		t.Errorf("expected added lines, got %q", stripped)
	}
	if !strings.Contains(stripped, " 1 a") {
		t.Errorf("expected context line, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_PreviewLimit(t *testing.T) {
	r := NewEditFileRenderer()
	var lines []string
	for i := 1; i <= editDiffPreviewLines+10; i++ {
		lines = append(lines, " line")
	}
	output := fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(lines), len(lines)) + strings.Join(lines, "\n") + "\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: false})
	if strings.Count(result, "\n") >= len(lines) {
		t.Errorf("expected preview truncation, got %d newlines", strings.Count(result, "\n"))
	}
	if !strings.Contains(ansi.Strip(result), "to expand") {
		t.Errorf("expected expand hint, got %q", ansi.Strip(result))
	}
}

func TestEditFileRenderer_RenderResult_NoDiff(t *testing.T) {
	r := NewEditFileRenderer()
	result := r.RenderResult("some plain status message", tuirender.RenderContext{Expanded: true})
	stripped := ansi.Strip(result)
	if stripped != "some plain status message" {
		t.Errorf("expected plain output, got %q", stripped)
	}
}

func TestEditFileRenderer_RenderResult_TabsExpanded(t *testing.T) {
	r := NewEditFileRenderer()
	output := "@@ -1,2 +1,2 @@\n-\told\n+\tnew\n"
	result := r.RenderResult(output, tuirender.RenderContext{Expanded: true})
	if strings.Contains(result, "\t") {
		t.Errorf("expected tabs expanded, got %q", result)
	}
	stripped := ansi.Strip(result)
	if !strings.Contains(stripped, "   old") || !strings.Contains(stripped, "   new") {
		t.Errorf("expected tab-expanded content, got %q", stripped)
	}
}

func TestEditFileRenderer_PreviewLines(t *testing.T) {
	r := NewEditFileRenderer()
	if got := r.PreviewLines(); got != editDiffPreviewLines {
		t.Errorf("PreviewLines = %d, want %d", got, editDiffPreviewLines)
	}
}

func TestEditFileRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := NewEditFileRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}

func TestEditRendererHelpers_UsePartialResets(t *testing.T) {
	for name, render := range map[string]func() string{
		"rDiffAdded":   func() string { return rDiffAdded("x") },
		"rDiffRemoved": func() string { return rDiffRemoved("x") },
		"rDiffContext": func() string { return rDiffContext("x") },
		"rInverse":     func() string { return rInverse("x") },
	} {
		t.Run(name, func(t *testing.T) {
			got := render()
			if strings.Contains(got, ansi.Reset) {
				t.Errorf("%s contains a full ANSI reset, which would kill an outer background color: %q", name, got)
			}
		})
	}
}
