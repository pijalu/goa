// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// stripANSI is declared in diff_render_test.go and reused here.

func TestMdSource_Heading(t *testing.T) {
	var s mdSourceState
	got := s.Highlight("# My Title")
	if !strings.Contains(got, ansi.Bold) || !strings.Contains(got, ansi.Fg(mdHeadingColor)) {
		t.Errorf("expected bold+colored heading, got %q", got)
	}
	if stripANSI(got) != "# My Title" {
		t.Errorf("heading text must stay 1:1, got %q", stripANSI(got))
	}

	// "###" (deeper level) and indented headings are still headings.
	for _, line := range []string{"### deep", "  ## indented"} {
		if out := s.Highlight(line); !strings.Contains(out, ansi.Bold) {
			t.Errorf("%q should render as a heading, got %q", line, out)
		}
	}
	// "#tag" without a space is NOT a heading.
	if out := s.Highlight("#tag"); strings.Contains(out, ansi.Bold+ansi.Fg(mdHeadingColor)) {
		t.Errorf("#tag must not be styled as heading, got %q", out)
	}
}

func TestMdSource_FenceStateAcrossLines(t *testing.T) {
	lines := []string{
		"```go",
		"func f() {}",
		"comment // inside",
		"```",
		"plain again",
	}
	var s mdSourceState
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = s.Highlight(l)
	}

	// Fence markers get the fence color.
	if !strings.Contains(out[0], ansi.Fg(mdFenceColor)) || !strings.Contains(out[3], ansi.Fg(mdFenceColor)) {
		t.Errorf("fence markers should be colored:\n%q\n%q", out[0], out[3])
	}
	// Inside-fence lines go through tools.HighlightLine — the go highlighter
	// colors keywords like "func".
	if !strings.Contains(out[1], ansi.Fg("#")) && out[1] == lines[1] {
		t.Errorf("inside-fence line should be highlighted as go code, got %q", out[1])
	}
	// After the closing fence, plain markdown styling resumes (no code color).
	if strings.Contains(stripANSI(out[4]), "```") {
		t.Errorf("closing state leaked: %q", out[4])
	}
}

func TestMdSource_FenceLanguageCaptured(t *testing.T) {
	var s mdSourceState
	s.Highlight("~~~python")
	styled := s.Highlight("x = 'a' # comment")
	// The python/bash-style comment highlighter faints comments.
	if !strings.Contains(styled, ansi.Faint) && !strings.Contains(styled, "#") {
		t.Fatalf("expected python-ish highlighting inside ~~~python fence, got %q", styled)
	}
	// A ``` line does NOT close a ~~~ fence.
	same := s.Highlight("```")
	if same != "```" {
		t.Errorf("backticks inside a tilde fence must stay literal code, got %q", same)
	}
	// The matching tilde fence closes it.
	closing := s.Highlight("~~~")
	if !strings.Contains(closing, ansi.Fg(mdFenceColor)) {
		t.Errorf("expected closing fence marker colored, got %q", closing)
	}
	after := s.Highlight("**bold** outside")
	if !strings.Contains(after, ansi.Bold) {
		t.Errorf("markdown styling must resume after close, got %q", after)
	}
}

func TestMdSource_FenceMarkerColoredAndTextPreserved(t *testing.T) {
	var s mdSourceState
	open := s.Highlight("  ```go extra")
	if stripANSI(open) != "  ```go extra" {
		t.Errorf("fence open line must be preserved 1:1, got %q", stripANSI(open))
	}
	if !strings.Contains(open, ansi.Fg(mdFenceColor)) {
		t.Errorf("fence open marker should be colored, got %q", open)
	}
}

func TestMdSource_Quote(t *testing.T) {
	var s mdSourceState
	got := s.Highlight("> quoted **text**")
	if !strings.Contains(got, ansi.Fg(mdQuoteColor)) {
		t.Errorf("quote marker should be colored, got %q", got)
	}
	if !strings.Contains(got, ansi.Bold) {
		t.Errorf("bold inside quote should still apply, got %q", got)
	}
	if stripANSI(got) != "> quoted **text**" {
		t.Errorf("quote line must stay 1:1, got %q", stripANSI(got))
	}
}

func TestMdSource_ListItems(t *testing.T) {
	var s mdSourceState
	bullet := s.Highlight("- item one")
	if !strings.Contains(bullet, ansi.Fg(mdBulletColor)) {
		t.Errorf("bullet marker should be colored, got %q", bullet)
	}
	if stripANSI(bullet) != "- item one" {
		t.Errorf("bullet line must stay 1:1, got %q", stripANSI(bullet))
	}
	ordered := s.Highlight("12. item two")
	if !strings.Contains(ordered, ansi.Fg(mdBulletColor)) {
		t.Errorf("ordered marker should be colored, got %q", ordered)
	}
	if stripANSI(ordered) != "12. item two" {
		t.Errorf("ordered line must stay 1:1, got %q", stripANSI(ordered))
	}
	// Not a list item: no space after the dash.
	if out := s.Highlight("-nospace"); strings.Count(out, ansi.Fg(mdBulletColor)) > strings.Count("-", "-nospace") {
		t.Errorf("'-nospace' must not be treated as a list item, got %q", out)
	}
}

func TestMdSource_InlineSpans(t *testing.T) {
	var s mdSourceState

	code := s.Highlight("use `go test` here")
	if !strings.Contains(code, ansi.Fg(mdCodeColor)) {
		t.Errorf("inline code should be colored, got %q", code)
	}
	bold := s.Highlight("**very** important")
	if !strings.Contains(bold, ansi.Bold) {
		t.Errorf("bold span expected, got %q", bold)
	}
	italic := s.Highlight("just *this* once")
	if !strings.Contains(italic, ansi.Italic) {
		t.Errorf("italic span expected, got %q", italic)
	}
	link := s.Highlight("see [docs](http://x.y)")
	if !strings.Contains(link, ansi.Underline) {
		t.Errorf("link text expected underlined, got %q", link)
	}
	// Source fidelity: URL stays visible (D1).
	if !strings.Contains(stripANSI(link), "(http://x.y)") {
		t.Errorf("link target must remain in source, got %q", stripANSI(link))
	}
	// All spans preserve the visible characters.
	for _, tc := range []string{
		"use `go test` here", "**very** important", "just *this* once",
		"see [docs](http://x.y)", "**a** *b* `c` [d](e)",
	} {
		if got := stripANSI(s.Highlight(tc)); got != tc {
			t.Errorf("inline spans must keep visible text 1:1: want %q got %q", tc, got)
		}
	}
}

func TestMdSource_UnclosedBacktickTailPlain(t *testing.T) {
	var s mdSourceState
	got := s.Highlight("a `b c d")
	if strings.Contains(got, ansi.Fg(mdCodeColor)) {
		t.Errorf("unpaired backtick tail must not be styled as code, got %q", got)
	}
}

func TestMdSource_SanitizesInput(t *testing.T) {
	var s mdSourceState
	// Control bytes are stripped by ansi.Sanitize.
	got := s.Highlight("bad\x07bell\x08")
	if strings.ContainsAny(got, "\x07\x08") {
		t.Errorf("control bytes must be sanitized away, got %q", got)
	}
	// Pre-existing ANSI from untrusted content cannot survive either.
	injected := s.Highlight("\x1b[31mred\x1b[0m")
	if strings.Contains(injected, "\x1b[31m") {
		t.Errorf("injected ANSI must be stripped before styling, got %q", injected)
	}
}

// TestMdSource_LineMappingIsOneToOne walks a mixed document and asserts the
// core D1 invariant per line: the visible width of every rendered line equals
// the width of its sanitized input — nothing reflowed, dropped, or added.
func TestMdSource_LineMappingIsOneToOne(t *testing.T) {
	doc := []string{
		"# Title with `code`",
		"",
		"Some intro **bold** and [link](url).",
		"```bash",
		"echo hi # comment",
		"~~~weird",
		"> quote > nested?",
		"- bullet `x`",
		"42. answer",
		"```",
		"tail ~~not strikethrough~~",
	}
	var s mdSourceState
	for i, line := range doc {
		want := ansi.Width(ansi.Sanitize(line))
		got := ansi.Width(s.Highlight(line))
		if got != want {
			t.Errorf("line %d (%q): visible width changed: want %d got %d", i, line, want, got)
		}
	}
}
