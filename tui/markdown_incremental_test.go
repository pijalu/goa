// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// The incremental renderer must produce byte-identical output to a full
// MDStreamRenderer render at every streaming step, across mixed markdown with
// multi-line blocks (paragraphs, code fences, lists, tables) split across
// chunk boundaries.
func TestIncrementalMD_MatchesFullRender(t *testing.T) {
	// A document with blocks that will be split across arbitrary chunk points.
	doc := "# Title\n\n" +
		"First paragraph with *bold* and `code`.\ncontinues here.\n\n" +
		"## Section\n\n" +
		"```go\nfunc main() {\n\t// comment\n\tprintln(\"hi\")\n}\n```\n\n" +
		"- item one\n- item two\n- item three\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\n" +
		"> a blockquote\n> spanning lines\n\n" +
		"Final paragraph.\n"

	full := NewMDStreamRenderer(120, TheTheme)
	incr := NewIncrementalMDRenderer(120, TheTheme)

	// Stream the doc in small chunks (including mid-word and mid-block splits).
	const chunk = 7
	for end := chunk; end <= len(doc)+chunk; end += chunk {
		e := end
		if e > len(doc) {
			e = len(doc)
		}
		text := doc[:e]
		got := incr.Render(text)
		want := full.Render(text)
		if !equalLines(got, want) {
			t.Fatalf("step end=%d: incremental != full render\n--- got ---\n%s\n--- want ---\n%s",
				e, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// An edit (non-append change) must fall back to a correct full render, not
// serve stale cached prefix.
func TestIncrementalMD_EditFallsBack(t *testing.T) {
	incr := NewIncrementalMDRenderer(120, TheTheme)
	full := NewMDStreamRenderer(120, TheTheme)

	_ = incr.Render("# Alpha\n\npara one\n\npara two\n\n")
	// Now change earlier content (not an append).
	edited := "# Beta\n\npara one\n\npara two CHANGED\n\nmore\n\n"
	got := incr.Render(edited)
	want := full.Render(edited)
	if !equalLines(got, want) {
		t.Fatalf("after edit, incremental != full render\n--- got ---\n%s\n--- want ---\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A document with NO blank-line boundary (single open block) must still render
// correctly (degrades to full render each frame, but correct).
func TestIncrementalMD_NoBoundary(t *testing.T) {
	incr := NewIncrementalMDRenderer(120, TheTheme)
	full := NewMDStreamRenderer(120, TheTheme)
	for _, text := range []string{"hello", "hello wor", "hello world `code`"} {
		if !equalLines(incr.Render(text), full.Render(text)) {
			t.Fatalf("no-boundary doc mismatch at %q", text)
		}
	}
}

// Fenced code with internal blank lines must not be split: the whole fence
// stays in the open tail until closed.
func TestIncrementalMD_FenceWithBlankLines(t *testing.T) {
	incr := NewIncrementalMDRenderer(120, TheTheme)
	full := NewMDStreamRenderer(120, TheTheme)
	doc := "before\n\n```txt\nline1\n\nline2\n\nline3\n```\n\nafter\n"
	const chunk = 5
	for end := chunk; end <= len(doc); end += chunk {
		e := end
		if e > len(doc) {
			e = len(doc)
		}
		text := doc[:e]
		if !equalLines(incr.Render(text), full.Render(text)) {
			t.Fatalf("fence-with-blanks mismatch at end=%d\ngot:\n%s\nwant:\n%s",
				e, strings.Join(incr.Render(text), "\n"), strings.Join(full.Render(text), "\n"))
		}
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
