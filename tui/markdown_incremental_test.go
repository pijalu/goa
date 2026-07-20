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

// TestBoundaryScanner_MatchesFullScan verifies that the incremental boundary
// scanner produces the same result as the original lastStableBoundary at
// every step of a growing document (B005 optimization correctness).
func TestBoundaryScanner_MatchesFullScan(t *testing.T) {
	docs := []string{
		// Simple paragraphs with blank lines.
		"# Title\n\npara one\n\npara two\n\npara three\n",
		// Fenced code with internal blanks.
		"before\n\n```go\ncode\n\nmore\n```\n\nafter\n",
		// Mixed: list, table, blockquote, fence.
		"# H\n\npara\n\n```go\ncode\n```\n\n- a\n- b\n\n| x |\n|---|\n| 1 |\n\n> q\n\nend\n",
		// No boundaries at all.
		"single line no newline",
		// Ends mid-line.
		"para\n\npartial",
	}

	for _, doc := range docs {
		var bs boundaryScanner
		// Feed the doc in 1-byte increments (worst case for the scanner).
		for i := 1; i <= len(doc); i++ {
			text := doc[:i]
			got := bs.advance(text)
			want := lastStableBoundary(text)
			if got != want {
				t.Errorf("doc %q at byte %d: incremental=%d, full=%d", doc[:20], i, got, want)
				break
			}
		}
	}
}

// TestBoundaryScanner_ResetOnShrink verifies the scanner resets correctly
// when text is replaced rather than appended.
func TestBoundaryScanner_ResetOnShrink(t *testing.T) {
	var bs boundaryScanner
	full := "para one\n\npara two\n\npara three\n"
	bs.advance(full)
	shorter := "para one\n\n"
	got := bs.advance(shorter)
	want := lastStableBoundary(shorter)
	if got != want {
		t.Errorf("after shrink: incremental=%d, full=%d", got, want)
	}
}

// TestIncrementalMD_StreamedFenceNeverFlattens is the regression for the
// reported rendering bug: a multi-line fenced code block streamed in
// multi-byte chunks was rendered as a single space-joined paragraph (fence
// markers dropped). Root cause: boundaryScanner.updateResumePos restored
// pre-chunk fence state whenever a chunk ended mid-line, losing a fence-open
// that completed in the same chunk; the next in-fence blank line was then
// accepted as a block boundary and the fence was split mid-content.
//
// The 1-byte-chunk tests never caught it because a 1-byte suffix that
// completes the fence line always ends with "\n" — the restore path was
// never taken with a pending fence toggle. Chunk sizes ≥ 4 bytes make the
// fence line and an incomplete following line share a chunk.
func TestIncrementalMD_StreamedFenceNeverFlattens(t *testing.T) {
	doc := "All clean and pushed. Here's the flow:\n\n## CLI Build & Release Flow\n\n```\nMakefile:\n  all: quality test build-cli        ← local dev (includes CLI)\n  release: quality test cross-build   ← local release (includes CLI)\n\nCI/CD:\n  quality-and-test ──┬──→ build-cli (matrix: 6 binaries) ──┐\n                     │                                      ├──→ release (on tags)\n                     └──→ compat-test ──────────────────────┘\n```\n\n**`build-cli` job** (matrix):\n- `cd cmd/frigolite` (the separate Go module)\n- Done."

	full := NewMDStreamRenderer(100, TheTheme)
	want := full.Render(doc)

	for _, chunk := range []int{1, 3, 4, 7, 13, 64} {
		incr := NewIncrementalMDRenderer(100, TheTheme)
		for end := chunk; ; end += chunk {
			e := end
			if e > len(doc) {
				e = len(doc)
			}
			text := doc[:e]
			got := incr.Render(text)
			if !equalLines(got, full.Render(text)) {
				t.Fatalf("chunk=%d end=%d: streamed render diverged from full render\ngot:\n%s\nwant:\n%s",
					chunk, e, strings.Join(got, "\n"), strings.Join(full.Render(text), "\n"))
			}
			if e >= len(doc) {
				break
			}
		}
		if !equalLines(incr.Render(doc), want) {
			t.Fatalf("chunk=%d: final render mismatch", chunk)
		}
	}
}

// TestBoundaryScanner_FenceOpenFollowedByIncompleteLine pins the scanner
// invariant directly: when a suffix contains a complete fence-open line
// followed by an incomplete content line, the fence state must survive into
// the next advance call (inFence=true at resumePos).
func TestBoundaryScanner_FenceOpenFollowedByIncompleteLine(t *testing.T) {
	var bs boundaryScanner
	// Chunk ends mid-line right after the fence opener completed.
	bs.advance("para\n\n```\nMakef")
	bs.advance("para\n\n```\nMakefile:\nline2\n\ninside blank\n")
	// The blank line after line2 is INSIDE the fence: no boundary past the
	// paragraph break may be created from it.
	full := lastStableBoundary("para\n\n```\nMakefile:\nline2\n\ninside blank\n")
	if bs.boundary != full {
		t.Errorf("boundary = %d, want %d (fence state lost across chunks)", bs.boundary, full)
	}
	if full > len("para\n\n") {
		t.Errorf("test invariant broken: full-doc boundary %d should stop at the paragraph break", full)
	}
}
