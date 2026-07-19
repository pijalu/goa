// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestMDStreamRenderer_Heading(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(40, theme)
	lines := r.Render("# Hello World")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "# Hello World") {
		t.Errorf("expected heading text, got %q", lines[0])
	}
	if !strings.Contains(lines[0], ansi.Bold) {
		t.Errorf("expected bold ANSI in heading, got %q", lines[0])
	}
}

func TestMDStreamRenderer_Paragraph(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(20, theme)
	lines := r.Render("This is a long paragraph that should wrap across multiple lines.")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping to produce multiple lines, got %d", len(lines))
	}
}

func TestMDStreamRenderer_CodeBlock(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(40, theme)
	input := "```go\nfmt.Println(\"hello\")\n```"
	lines := r.Render(input)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (blank + lang + code + blank), got %d: %v", len(lines), lines)
	}
	if ansi.Strip(lines[0]) != "" {
		t.Errorf("expected blank line before code block, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "go") {
		t.Errorf("expected language line, got %q", lines[1])
	}
	// Strip ANSI codes before checking content
	clean := ansi.Strip(lines[2])
	if !strings.Contains(clean, "fmt.Println") {
		t.Errorf("expected code line containing fmt.Println, got %q (clean: %q)", lines[2], clean)
	}
	if ansi.Strip(lines[3]) != "" {
		t.Errorf("expected blank line after code block, got %q", lines[3])
	}
}

func TestMDStreamRenderer_Blockquote(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(40, theme)
	lines := r.Render("> This is a quote")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "│ ") {
		t.Errorf("expected blockquote prefix, got %q", lines[0])
	}
}

func TestMDStreamRenderer_List(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(40, theme)
	input := "- first item\n- second item"
	lines := r.Render(input)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "• ") {
		t.Errorf("expected bullet prefix, got %q", lines[0])
	}
}

func TestMDStreamRenderer_ThematicBreak(t *testing.T) {
	theme := DarkTheme()
	r := NewMDStreamRenderer(20, theme)
	lines := r.Render("---")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "─") {
		t.Errorf("expected separator line, got %q", lines[0])
	}
}

func TestRenderInline_Bold(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("**hello**", theme)
	want := ansi.Bold + "hello" + ansi.Reset
	if got != want {
		t.Errorf("renderInline bold = %q, want %q", got, want)
	}
}

func TestRenderInline_Italic(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("*hello*", theme)
	want := ansi.Italic + "hello" + ansi.Reset
	if got != want {
		t.Errorf("renderInline italic = %q, want %q", got, want)
	}
}

func TestRenderInline_Code(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("`code`", theme)
	if !strings.Contains(got, "code") {
		t.Errorf("renderInline code should contain 'code', got %q", got)
	}
}

func TestRenderInline_Strikethrough(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("~~deleted~~", theme)
	want := ansi.Strikethrough + "deleted" + ansi.Reset
	if got != want {
		t.Errorf("renderInline strikethrough = %q, want %q", got, want)
	}
}

func TestRenderInline_ItalicUnderscore(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("_hello_", theme)
	want := ansi.Italic + "hello" + ansi.Reset
	if got != want {
		t.Errorf("renderInline _italic_ = %q, want %q", got, want)
	}
}

// TestRenderInline_ItalicUnderscoreNotIntraWord covers the CommonMark flanking
// rule: intra-word underscores (snake_case) are NOT emphasis and must stay
// literal — otherwise identifiers like KEEP_VISIBLE_TOP get mangled.
func TestRenderInline_ItalicUnderscoreNotIntraWord(t *testing.T) {
	theme := DarkTheme()
	for _, in := range []string{"KEEP_VISIBLE_TOP", "foo_bar_baz", "a_b_c"} {
		if got := renderInline(in, theme); got != in {
			t.Errorf("renderInline(%q) = %q, want unchanged (intra-word _ is not italic)", in, got)
		}
	}
	// But spaced underscores still italicize.
	if got := renderInline("a _slanted_ word", theme); !strings.Contains(got, ansi.Italic) {
		t.Errorf("spaced _slanted_ should italicize, got %q", got)
	}
}

// TestRenderInline_FontStyleGate verifies the config-driven font-style gate:
// a disabled style emits no SGR (text passes through with markers stripped to
// the gated-empty sequence), while enabled styles emit their codes.
func TestRenderInline_FontStyleGate(t *testing.T) {
	theme := DarkTheme()
	cases := []struct {
		name    string
		gate    ansi.FontStyles
		input   string
		wantSeq string // expected SGR ("" = style suppressed)
	}{
		{"bold on", ansi.FontStyles{Bold: true}, "**x**", ansi.Bold},
		{"bold off", ansi.FontStyles{}, "**x**", ""},
		{"italic on", ansi.FontStyles{Italic: true}, "*x*", ansi.Italic},
		{"italic off", ansi.FontStyles{}, "*x*", ""},
		{"italic underscore on", ansi.FontStyles{Italic: true}, "_x_", ansi.Italic},
		{"strike on", ansi.FontStyles{Strikethrough: true}, "~~x~~", ansi.Strikethrough},
		{"strike off", ansi.FontStyles{}, "~~x~~", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := ansi.ActiveFontStyles()
			ansi.SetFontStyles(tc.gate)
			defer ansi.SetFontStyles(prev)
			got := renderInline(tc.input, theme)
			if tc.wantSeq == "" {
				// Style suppressed: the raw SGR for that style must not appear.
				if strings.Contains(got, "\x1b[1m") || strings.Contains(got, "\x1b[3m") || strings.Contains(got, "\x1b[9m") {
					t.Errorf("style should be suppressed, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSeq) {
				t.Errorf("renderInline(%q) missing %q, got %q", tc.input, tc.wantSeq, got)
			}
		})
	}
}

func TestRenderInline_Link(t *testing.T) {
	theme := DarkTheme()
	got := renderInline("[text](http://example.com)", theme)
	if !strings.Contains(got, "text") {
		t.Errorf("renderInline link should contain 'text', got %q", got)
	}
	if strings.Contains(got, "http://example.com") {
		t.Errorf("renderInline link should not contain URL, got %q", got)
	}
}

func TestRenderInline_Incomplete(t *testing.T) {
	theme := DarkTheme()
	// Incomplete constructs should be left as plain text
	got := renderInline("**hello", theme)
	if got != "**hello" {
		t.Errorf("incomplete bold = %q, want plain text", got)
	}
	got = renderInline("*hello", theme)
	if got != "*hello" {
		t.Errorf("incomplete italic = %q, want plain text", got)
	}
	got = renderInline("`code", theme)
	if got != "`code" {
		t.Errorf("incomplete code = %q, want plain text", got)
	}
}

func TestRenderInline_Escapes(t *testing.T) {
	theme := DarkTheme()
	// Backslash escape removes the backslash; subsequent formatting may apply
	got := renderInline("hello \\*world*", theme)
	// The backslash is stripped, then *world* becomes italic
	if !strings.Contains(got, "world") {
		t.Errorf("renderInline escape should contain 'world', got %q", got)
	}
}

func TestIsThematicBreak(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"---", true},
		{"***", true},
		{"___", true},
		{" - - - ", false},
		{"--", false},
		{"", false},
		{"--- ", true},
		{" hello", false},
	}
	for _, tt := range tests {
		got := isThematicBreak(tt.input)
		if got != tt.want {
			t.Errorf("isThematicBreak(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHeadingLevel(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"# Hello", 1},
		{"## Hello", 2},
		{"###### Hello", 6},
		{"####### Hello", 0},
		{"Hello", 0},
		{"#Hello", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := headingLevel(tt.input)
		if got != tt.want {
			t.Errorf("headingLevel(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestIsUnorderedListItem(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"- item", true},
		{"* item", true},
		{"+ item", true},
		{"item", false},
		{"-", false},
		{"  - item", true},
	}
	for _, tt := range tests {
		got := isUnorderedListItem(tt.input)
		if got != tt.want {
			t.Errorf("isUnorderedListItem(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsOrderedListItem(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1. item", true},
		{"10. item", true},
		{"item", false},
		{"1.", false},
		{"  2. item", true},
	}
	for _, tt := range tests {
		got := isOrderedListItem(tt.input)
		if got != tt.want {
			t.Errorf("isOrderedListItem(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestStripListMarker(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"- hello", "hello"},
		{"* hello", "hello"},
		{"+ hello", "hello"},
		{"1. hello", "hello"},
		{"10. hello", "hello"},
		{"  - hello", "hello"},
	}
	for _, tt := range tests {
		got := stripListMarker(tt.input)
		if got != tt.want {
			t.Errorf("stripListMarker(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFindPair(t *testing.T) {
	content, start, end, ok := findPair("hello **world** foo", "**", "**", 0)
	if !ok {
		t.Fatal("expected to find pair")
	}
	if content != "world" {
		t.Errorf("content = %q, want 'world'", content)
	}
	if start != 6 {
		t.Errorf("start = %d, want 6", start)
	}
	if end != 15 {
		t.Errorf("end = %d, want 15", end)
	}

	_, _, _, ok = findPair("**incomplete", "**", "**", 0)
	if ok {
		t.Error("expected no pair for incomplete construct")
	}
}

func TestRenderInline_BoldLink_NoAnsiLeak(t *testing.T) {
	theme := DarkTheme()
	// Bold wrapping around a link: **[text](url)**
	// Previously, renderInlineBold would insert \\x1b[1m before [text, and
	// renderInlineLinks would mistake the [ inside \\x1b[1m for a link bracket.
	got := renderInline("**[text](url)**", theme)
	// Should NOT contain literal "1m[" or "1m]" artifacts (ANSI corruption)
	if strings.Contains(got, "1m[") || strings.Contains(got, "1m]") {
		t.Errorf("should not contain ANSI artifact '1m[' or '1m]', got %q", got)
	}
	// Should contain the link text (bold-rendered)
	if !strings.Contains(got, "text") {
		t.Errorf("should render link text 'text', got %q", got)
	}
	// Should contain bold ANSI code (the outer ** wrapping)
	if !strings.Contains(got, ansi.Bold) {
		t.Errorf("should have bold wrapping, got %q", got)
	}
}

func TestRenderInline_LinkAfterBold(t *testing.T) {
	theme := DarkTheme()
	// Bold text followed by a link on the same line
	got := renderInline("**bold** and [link](url)", theme)
	if strings.Contains(got, "1m[") || strings.Contains(got, "1m]") {
		t.Errorf("should not contain ANSI artifact, got %q", got)
	}
	if !strings.Contains(got, "bold") {
		t.Errorf("should contain bold text, got %q", got)
	}
}

func TestRenderInline_ListWithBoldLink(t *testing.T) {
	theme := DarkTheme()
	// Simulates a list item with bold link (as seen in MD stream rendering)
	got := renderInline("- **[text](url)** description", theme)
	if strings.Contains(got, "1m[") || strings.Contains(got, "1m]") {
		t.Errorf("should not contain ANSI artifact, got %q", got)
	}
	if !strings.Contains(got, "text") {
		t.Errorf("should contain link text, got %q", got)
	}
}

func TestTranslateEntities(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"rightarrow", "$rightarrow$", "→"},
		{"leftarrow", "$leftarrow$", "←"},
		{"alpha", "$alpha$", "α"},
		{"beta", "$beta$", "β"},
		{"infty", "$infty$", "∞"},
		{"neq", "$neq$", "≠"},
		{"bullet", "$bullet$", "•"},
		{"no_match", "regular text", "regular text"},
		{"mixed", "x $rightarrow$ y $alpha$ z", "x → y α z"},
		{"adjacent", "$alpha$$beta$", "αβ"},
		{"empty", "", ""},
		// LaTeX math mode patterns
		{"ge_percent", "$\\ge 90\\%$", "≥ 90%"},
		{"ge", "$\\ge$", "≥"},
		{"alpha_beta", "$\\alpha + \\beta$", "α + β"},
		{"mixed_latex_and_entity", "x $\\ge$ 5 $rightarrow$ y", "x ≥ 5 → y"},
		{"geq_latex", "$\\geq$", "≥"},
		{"leq_latex", "$\\leq$", "≤"},
		{"ne_latex", "$\\ne$", "≠"},
		{"to_latex", "$\\to$", "→"},
		{"infty_latex", "$\\infty$", "∞"},
		{"delta_latex", "$\\delta$", "δ"},
		{"percent_via_escape", "$90\\%$", "90%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateEntities(tt.input)
			if got != tt.want {
				t.Errorf("translateEntities(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderInline_EntityTranslation(t *testing.T) {
	theme := DarkTheme()
	// Entity translation should happen before inline rendering
	got := renderInline("x $rightarrow$ y", theme)
	if strings.Contains(got, "$rightarrow$") {
		t.Errorf("expected entity to be translated, got raw $rightarrow$ in %q", got)
	}
}

func TestRenderInline_EntitiesInParagraph(t *testing.T) {
	theme := DarkTheme()
	// Verify entity translation works within paragraph rendering
	r := NewMDStreamRenderer(80, theme)
	lines := r.Render("The symbol $rightarrow$ means implies.")
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	for _, line := range lines {
		if strings.Contains(line, "$rightarrow$") {
			t.Errorf("expected $rightarrow$ to be translated, got raw in %q", line)
		}
		if !strings.Contains(line, "→") {
			t.Errorf("expected arrow character → in output, got %q", line)
		}
	}
}

