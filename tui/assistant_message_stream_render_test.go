// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// streamedMarkdownDoc is a markdown answer exercised chunk-by-chunk: paragraphs,
// headings, list items, tables, blockquotes and fences (including a fence with
// blank lines, the boundary case incremental splitting must never cut through).
var streamedMarkdownDoc = []string{
	"Intro paragraph with **bold** and `inline code` that wraps on a narrow terminal.",
	"",
	"## Heading",
	"",
	"- first list item",
	"- second list item",
	"",
	"```go",
	"func one() int {",
	"",
	"	return 1",
	"}",
	"```",
	"",
	"| col a | col b |",
	"| --- | --- |",
	"| 1 | 2 |",
	"",
	"> a blockquote line",
	"> continues here",
	"",
	"Closing paragraph with an emoji 👨‍👩‍👧 and wide text 日本語 to exercise widths.",
}

// TestAssistantMessage_StreamedRenderMatchesOneShot is the correctness gate for
// the incremental padding cache: rendering a document as a growing append stream
// must be byte-identical, at EVERY step, to rendering the same text from scratch.
func TestAssistantMessage_StreamedRenderMatchesOneShot(t *testing.T) {
	docs := map[string][]string{
		"blocks":    streamedMarkdownDoc,
		"paragraph": unbrokenParagraphDoc(),
		"mixed":     mixedNoBlankDoc(),
	}
	for name, doc := range docs {
		for _, width := range []int{40, 80, 120} {
			t.Run(fmt.Sprintf("%s/width%d", name, width), func(t *testing.T) {
				assertStreamMatchesOneShot(t, doc, width)
			})
		}
	}
}

// assertStreamMatchesOneShot appends doc line by line and compares every frame
// against a from-scratch render of the same text.
func assertStreamMatchesOneShot(t *testing.T, doc []string, width int) {
	t.Helper()
	streamed := newAssistantMessage("")
	var text strings.Builder
	for i, line := range doc {
		if i > 0 {
			text.WriteString("\n")
		}
		text.WriteString(line)
		streamed.SetText(text.String())

		got := streamed.Render(width)
		want := newAssistantMessage(text.String()).Render(width)
		assertSameLines(t, i, got, want)
	}
}

// unbrokenParagraphDoc is the pathological streaming shape: one paragraph with
// no blank line, so no blank-line block boundary ever appears. Every line is
// still append-stable, and the renderer must exploit that.
func unbrokenParagraphDoc() []string {
	doc := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		doc = append(doc, fmt.Sprintf("line %d of a single unbroken paragraph with `code`, **bold** and text", i))
	}
	return doc
}

// mixedNoBlankDoc interleaves list items, quotes and headings without blank
// separators — the shapes whose rendering spans lines and must NOT be cut.
func mixedNoBlankDoc() []string {
	return []string{
		"intro line one",
		"intro line two",
		"- item a",
		"- item b",
		"> quote line",
		"> quote second",
		"plain again",
		"## heading",
		"body after heading",
		"another body line",
		"```go",
		"code one",
		"code two",
		"```",
		"| a | b |",
		"| --- | --- |",
		"| 1 | 2 |",
		"tail line",
	}
}

// TestAssistantMessage_StreamedRenderAfterEditAndResize covers the invalidation
// paths: a mid-stream edit (non-append change) and a width change must both fall
// back to a correct full render.
func TestAssistantMessage_StreamedRenderAfterEditAndResize(t *testing.T) {
	streamed := newAssistantMessage("")
	for _, line := range streamedMarkdownDoc[:10] {
		streamed.SetText(streamed.text + line + "\n")
		streamed.Render(80)
	}
	// Edit (shrink) mid-stream: the incremental prefix no longer applies.
	edited := strings.Join(streamedMarkdownDoc[:4], "\n")
	streamed.SetText(edited)
	assertSameLines(t, 0, streamed.Render(80), newAssistantMessage(edited).Render(80))

	// Resize: same text at a new width must match a fresh render at that width.
	assertSameLines(t, 1, streamed.Render(57), newAssistantMessage(edited).Render(57))

	// Finish metadata still appends after the cached prefix.
	streamed.SetFinishReason("stop", 42, 1500)
	got := streamed.Render(80)
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "42 tok") {
		t.Fatalf("finish line missing after cached-prefix render")
	}
}

// TestAssistantMessage_StreamingPadWorkDoesNotGrowWithAnswer pins the actual
// CPU fix: the number of lines padded per Render must be bounded by the lines
// that are not yet memoized, not by the answer length.
func TestAssistantMessage_StreamingPadWorkDoesNotGrowWithAnswer(t *testing.T) {
	m := newAssistantMessage("")
	const width = 80
	var text strings.Builder
	for i := 0; i < 400; i++ {
		if i > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(fmt.Sprintf("paragraph %d with a reasonable amount of text in it", i))
		m.SetText(text.String())

		before := m.pads.src
		m.Render(width)
		padded := m.pads.src - before
		// Growth per chunk is bounded by the new paragraph's own lines (+1 for
		// the separator), never by the document length.
		if padded > 4 {
			t.Fatalf("chunk %d padded %d previously-stable lines: prefix cache not reused", i, padded)
		}
	}
	// The final paragraph is the open tail, so almost the whole document is
	// memoized: everything but the last block.
	if m.pads.src < 395 {
		t.Fatalf("stable prefix covered %d lines, expected the document to stabilize", m.pads.src)
	}
}

func assertSameLines(t *testing.T, step int, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step %d: %d lines, want %d\n got: %q\nwant: %q", step, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d line %d:\n got: %q\nwant: %q", step, i, got[i], want[i])
		}
	}
}
