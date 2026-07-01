// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestSearchRenderer_RenderCall(t *testing.T) {
	r := NewSearchRenderer()

	tests := []struct {
		name     string
		args     map[string]any
		wantPats []string // substrings the result should contain
	}{
		{
			name:     "simple pattern",
			args:     map[string]any{"pattern": "TODO"},
			wantPats: []string{"TODO"},
		},
		{
			name:     "pattern with glob",
			args:     map[string]any{"pattern": "TODO", "glob": "*.go"},
			wantPats: []string{"TODO", "*.go"},
		},
		{
			name:     "pattern with path",
			args:     map[string]any{"pattern": "TODO", "path": "src/"},
			wantPats: []string{"TODO", "src/"},
		},
		{
			name:     "pattern with glob and path",
			args:     map[string]any{"pattern": "TODO", "glob": "*.go", "path": "src/"},
			wantPats: []string{"TODO", "*.go", "src/"},
		},
		{
			name:     "case-sensitive",
			args:     map[string]any{"pattern": "TODO", "case_sensitive": true},
			wantPats: []string{"TODO", "case-sensitive"},
		},
		{
			name:     "non-recursive",
			args:     map[string]any{"pattern": "TODO", "recursive": false},
			wantPats: []string{"TODO", "non-recursive"},
		},
		{
			name:     "recursive default (true)",
			args:     map[string]any{"pattern": "TODO", "recursive": true},
			wantPats: []string{"TODO"},
		},
		{
			name:     "with max_results",
			args:     map[string]any{"pattern": "TODO", "max_results": float64(10)},
			wantPats: []string{"TODO", "max:10"},
		},
		{
			name:     "with exclude_glob",
			args:     map[string]any{"pattern": "TODO", "exclude_glob": "*_test.go"},
			wantPats: []string{"TODO", "*_test.go"},
		},
		{
			name:     "all options",
			args:     map[string]any{"pattern": "TODO", "glob": "*.go", "exclude_glob": "*_test.go", "path": "src/", "case_sensitive": true, "recursive": false, "max_results": float64(10)},
			wantPats: []string{"TODO", "*.go", "*_test.go", "src/", "case-sensitive", "non-recursive", "max:10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.RenderCall(tt.args, tuirender.RenderContext{})
			if result == "" {
				t.Error("expected non-empty result")
			}
			for _, want := range tt.wantPats {
				if !strings.Contains(result, want) {
					t.Errorf("result %q should contain %q", result, want)
				}
			}
		})
	}
}

func TestSearchRenderer_RenderResult(t *testing.T) {
	r := NewSearchRenderer()

	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "no matching files",
			output:   "[search: no matching files found]",
			expected: "search",
		},
		{
			name:     "no matches in files",
			output:   `[search: "pattern"] — no matches found`,
			expected: "search",
		},
		{
			name: "one match",
			output: `[search: "TODO"] — 1 matches found
main.go:5: // TODO: fix this`,
			expected: "search",
		},
		{
			name: "multiple matches",
			output: `[search: "TODO"] — 3 matches found
main.go:5: // TODO: fix this
main.go:10: // TODO: add tests
utils.go:20: // TODO: refactor`,
			expected: "search",
		},
		{
			name: "truncated results",
			output: `[search: "TODO"] — 10 matches found, showing 5 (5 truncated)
main.go:5: // TODO: fix this
main.go:10: // TODO: add tests`,
			expected: "search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.RenderResult(tt.output, tuirender.RenderContext{})
			if result == "" {
				t.Error("expected non-empty result")
			}
		})
	}
}

func TestSearchRenderer_PreviewLines(t *testing.T) {
	r := NewSearchRenderer()
	if r.PreviewLines() != 20 {
		t.Errorf("expected 20 preview lines, got %d", r.PreviewLines())
	}
}

func TestSearchRenderer_HideResultWhenCollapsed(t *testing.T) {
	r := NewSearchRenderer()
	if r.HideResultWhenCollapsed() {
		t.Error("expected false")
	}
}
