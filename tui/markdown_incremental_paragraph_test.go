// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestIncrementalMD_UnbrokenParagraphStabilizes is the CPU fix at the source:
// a single long paragraph (no blank lines, so no blank-line block boundary) must
// still let the renderer cache a growing stable prefix. Without paragraph-level
// cut points the whole answer re-parses on every chunk.
func TestIncrementalMD_UnbrokenParagraphStabilizes(t *testing.T) {
	ir := NewIncrementalMDRenderer(80, TheTheme)
	var text strings.Builder
	lastStable := 0
	for i := 0; i < 40; i++ {
		if i > 0 {
			text.WriteString("\n")
		}
		text.WriteString(fmt.Sprintf("paragraph line %d with some words in it", i))
		out := ir.Render(text.String())
		// Byte-identical to a one-shot render at every step.
		want := NewMDStreamRenderer(80, TheTheme).Render(text.String())
		if strings.Join(out, "\n") != strings.Join(want, "\n") {
			t.Fatalf("step %d: incremental output diverged from one-shot\n got: %q\nwant: %q", i, out, want)
		}
		if i > 1 && ir.StableLineCount() < lastStable {
			t.Fatalf("step %d: stable prefix shrank (%d -> %d)", i, lastStable, ir.StableLineCount())
		}
		lastStable = ir.StableLineCount()
	}
	if lastStable < 30 {
		t.Fatalf("stable prefix = %d lines after 40-line paragraph, want most of it cached", lastStable)
	}
}

// TestIncrementalMD_SpanningBlocksDoNotStabilizeInside guards the correctness
// side of the cut rule: lists, blockquotes, tables and fences render across
// lines, so no cut may be taken inside an open one.
func TestIncrementalMD_SpanningBlocksDoNotStabilizeInside(t *testing.T) {
	cases := map[string]string{
		"list":  "- item one\n- item two\n- item three\n",
		"quote": "> quote one\n> quote two\n> quote three\n",
		"table": "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		"fence": "```go\nline one\nline two\n```\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			ir := NewIncrementalMDRenderer(80, TheTheme)
			lines := strings.SplitAfter(doc, "\n")
			var text strings.Builder
			for i, line := range lines {
				text.WriteString(line)
				out := ir.Render(text.String())
				want := NewMDStreamRenderer(80, TheTheme).Render(text.String())
				if strings.Join(out, "\n") != strings.Join(want, "\n") {
					t.Fatalf("%s step %d: incremental output diverged\n got: %q\nwant: %q", name, i, out, want)
				}
			}
		})
	}
}

// TestStandaloneBlockLine pins the cut-safety classification per line kind.
func TestStandaloneBlockLine(t *testing.T) {
	cases := map[string]bool{
		"plain paragraph line": true,
		"## heading":           true,
		"---":                  true,
		"```go":                false,
		"```":                  false,
		"- list item":          false,
		"* list item":          false,
		"1. ordered item":      false,
		"> quote":              false,
		"| a | b |":            false,
		"| --- | --- |":        false,
		"":                     false,
		"   ":                  false,
		"█ graph bar":          false,
	}
	for line, want := range cases {
		if got := standaloneBlockLine(line); got != want {
			t.Errorf("standaloneBlockLine(%q) = %v, want %v", line, got, want)
		}
	}
}
