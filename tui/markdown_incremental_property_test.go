// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// Property: for a wide variety of markdown docs and every split point,
// incremental render == full render. This is the byte-identical invariant that
// makes the streaming optimization safe across block types (paragraphs, code
// fences, lists, tables, blockquotes) and their blank-line-absorption rules.
func TestIncrementalMD_Property(t *testing.T) {
	docs := []string{
		"# H\n\npara\n\n```go\ncode\n```\n\n- a\n- b\n\n| x |\n|---|\n| 1 |\n\n> q\n\nend\n",
		"para one line\n\npara two\n\n\n\ndouble blank\n\n",
		"```\n\n\n```\n\nafter\n",       // fence with internal blanks
		"> quote\n\n> more quote\n\n",     // quote absorbing blank
		"- list\n\n- continued\n\npara\n", // list absorbing blank
		"| a |\n|-|\n| b |\n\ntext\n",    // table
		"no boundaries at all here",
		"\n\n\nleading blanks\n\n",
		"# only heading",
		"text\n\n", // trailing blank
	}
	for di, doc := range docs {
		full := NewMDStreamRenderer(120, TheTheme)
		incr := NewIncrementalMDRenderer(120, TheTheme)
		for end := 1; end <= len(doc); end++ {
			text := doc[:end]
			if g, w := incr.Render(text), full.Render(text); !equalLines(g, w) {
				t.Fatalf("doc %d end=%d mismatch\ndoc=%q\ngot %d lines, want %d\ngot:\n%s\nwant:\n%s",
					di, end, doc, len(g), len(w), strings.Join(g, "\n"), strings.Join(w, "\n"))
			}
		}
		for range 3 {
			if g, w := incr.Render(doc), full.Render(doc); !equalLines(g, w) {
				t.Fatalf("doc %d final re-render mismatch", di)
			}
		}
	}
}
