// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestRenderInline_LaTeXMathInParagraph(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(80, theme)

	tests := []struct {
		name  string
		input string
		check func(t *testing.T, lines []string)
	}{
		{name: "ge_percent", input: "Target: $\\ge 90\\%$ coverage.", check: checkGePercent},
		{name: "mixed_latex_entity", input: "x $\\ge$ 5 $rightarrow$ implies $\\alpha$", check: checkMixedLatexEntity},
		{name: "just_percent_escape", input: "Value: $90\\%$ done.", check: checkJustPercentEscape},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := r.Render(tt.input)
			if len(lines) == 0 {
				t.Fatal("expected rendered lines")
			}
			tt.check(t, lines)
		})
	}
}

func checkGePercent(t *testing.T, lines []string) {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, "$\\ge") {
			t.Errorf("expected $\\ge to be translated, got raw in %q", line)
		}
		if !strings.Contains(line, "≥") {
			t.Errorf("expected ≥ character in output, got %q", line)
		}
		if !strings.Contains(line, "%") {
			t.Errorf("expected %% character in output, got %q", line)
		}
	}
}

func checkMixedLatexEntity(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	combined := strings.Join(lines, " ")
	if strings.Contains(combined, "$\\ge") || strings.Contains(combined, "$rightarrow$") || strings.Contains(combined, "$\\alpha") {
		t.Errorf("expected all entities to be translated, got raw markers in %q", combined)
	}
	if !strings.Contains(combined, "→") {
		t.Errorf("expected → character in output, got %q", combined)
	}
	if !strings.Contains(combined, "α") {
		t.Errorf("expected α character in output, got %q", combined)
	}
}

func checkJustPercentEscape(t *testing.T, lines []string) {
	t.Helper()
	combined := strings.Join(lines, " ")
	if strings.Contains(combined, "$90\\%$") {
		t.Errorf("expected $90\\%%$ to be translated, got raw in %q", combined)
	}
	if !strings.Contains(combined, "90%") {
		t.Errorf("expected '90%%' in output, got %q", combined)
	}
}
