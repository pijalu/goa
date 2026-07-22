// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pijalu/goa/internal/ansi"
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

// TestSearchRenderer_ResultPatternNotDoubleEscaped is the bugs.md item J
// regression: the search tool emits its header pattern Go-quoted (%q), so a
// regex pattern containing a backslash escape (e.g. `SelectOption\(`) must
// render byte-identical in the summary — not with a doubled backslash.
func TestSearchRenderer_ResultPatternNotDoubleEscaped(t *testing.T) {
	r := NewSearchRenderer()
	// Exactly what tools/search.go produces for pattern `func .*SelectOption\(`.
	output := "[search: \"func .*SelectOption\\\\(\"] — 2 matches across 2 files\n" +
		"core/context.go:435: func (c Context) SelectOption\n" +
		"core/commands/x.go:159: func (f *fake) SelectOption"
	result := r.RenderResult(output, tuirender.RenderContext{})
	plain := ansi.Strip(result)
	if !strings.Contains(plain, `func .*SelectOption\(`) {
		t.Errorf("pattern must render byte-identical (single backslash), got:\n%s", plain)
	}
	if strings.Contains(plain, `SelectOption\\(`) {
		t.Errorf("pattern rendered with doubled backslash (item J):\n%s", plain)
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

// TestSearchRenderer_RenderResult_EscapesControlBytes: even if tool output
// ever carries raw ESC bytes, the renderer must never forward them to the
// terminal — a stray clear-line sequence would corrupt the whole frame.
func TestSearchRenderer_RenderResult_EscapesControlBytes(t *testing.T) {
	r := NewSearchRenderer()
	output := "[search: \"main\"] — 1 matches found\nlog.txt: 1 matches\n  16: repo (\x1b[38;2;63;185;80m⎇ main\x1b[0m)"
	result := r.RenderResult(output, tuirender.RenderContext{})
	// The renderer adds its own theme colors; what must not leak is the
	// *file's* raw sequence (ESC + "[38;2;63;185;80m").
	if strings.Contains(result, "\x1b[38;2;63;185;80m") {
		t.Errorf("raw file ESC sequence leaked into render: %q", result)
	}
	if !strings.Contains(result, `\e[38;2;63;185;80m`) {
		t.Errorf("expected escaped sequence as literal text, got: %q", result)
	}
}

// TestSearchRenderer_RenderResult_TruncatesRuneSafe: the 80-column preview
// cut must not split a multi-byte rune (byte cuts render as '�').
func TestSearchRenderer_RenderResult_TruncatesRuneSafe(t *testing.T) {
	r := NewSearchRenderer()
	long := strings.Repeat("世", 100) // 3 bytes each: byte cut at 80 lands mid-rune
	output := "[search: \"x\"] — 1 matches found\nu.txt: 1 matches\n  7: " + long
	result := r.RenderResult(output, tuirender.RenderContext{})
	if !utf8.ValidString(ansi.Strip(result)) {
		t.Errorf("renderer output is not valid UTF-8 (rune split): %q", result)
	}
}
