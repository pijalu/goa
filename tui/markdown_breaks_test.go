// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// Regression: single newlines sent by the model must survive markdown
// rendering (GFM "breaks: true"). Previously renderParagraph space-joined
// paragraph lines, so a line break visible at a stream edge vanished once
// more text arrived without a blank line.

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(ansi.Strip(l))
	}
	return out
}

func TestMDStreamRenderer_PreservesSoftBreaks(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := plainLines(r.Render("step one\nstep two\nstep three"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 visual lines for 3 source lines, got %d: %q", len(lines), lines)
	}
	want := []string{"step one", "step two", "step three"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

// The streaming fix: rendering "alpha\n" then "alpha\nbeta" must be
// append-only — earlier lines keep their exact bytes instead of re-flowing.
func TestMDStreamRenderer_StreamAppendStable(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	frame1 := r.Render("alpha\n")
	if len(frame1) != 1 || !strings.Contains(ansi.Strip(frame1[0]), "alpha") {
		t.Fatalf("frame1 = %v, want single alpha line", frame1)
	}
	frame2 := r.Render("alpha\nbeta")
	if len(frame2) != 2 {
		t.Fatalf("frame2 = %v, want two lines (newline preserved)", frame2)
	}
	for i := range frame1 {
		if frame1[i] != frame2[i] {
			t.Errorf("line %d re-flowed:\nframe1: %q\nframe2: %q", i, frame1[i], frame2[i])
		}
	}
}

// A long source line must still soft-wrap within the renderer width.
func TestMDStreamRenderer_LongLineStillWraps(t *testing.T) {
	r := NewMDStreamRenderer(20, DarkTheme())
	long := strings.Repeat("word ", 20) // 100 chars, no newline
	lines := r.Render(long)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping of long paragraph, got %d lines", len(lines))
	}
	for _, l := range lines {
		if ansi.Width(ansi.Strip(l)) > 20 {
			t.Errorf("line exceeds width: %q", ansi.Strip(l))
		}
	}
}

func TestMDStreamRenderer_ParagraphStopsAtBlankLine(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := plainLines(r.Render("para one\n\npara two"))
	if len(lines) != 2 || lines[0] != "para one" || lines[1] != "para two" {
		t.Fatalf("got %q, want [para one para two]", lines)
	}
}

func TestMDStreamRenderer_CRLFTrimmed(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := plainLines(r.Render("first\r\nsecond\r"))
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("got %q, want [first second] without CR remnants", lines)
	}
}

// List continuation lines keep their own visual line under the bullet.
func TestMDStreamRenderer_ListContinuationPreserved(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := plainLines(r.Render("- item one\ncontinued text"))
	if len(lines) != 2 {
		t.Fatalf("expected marker line + continuation line, got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "• item one") {
		t.Errorf("line 0 = %q, want bullet + item text", lines[0])
	}
	if lines[1] != "continued text" {
		t.Errorf("line 1 = %q, want continuation on its own line", lines[1])
	}
}

func TestMDStreamRenderer_OrderedListContinuationPreserved(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := plainLines(r.Render("1. first\n2. second\nnote under second"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "1. first") || !strings.HasPrefix(lines[1], "2. second") {
		t.Errorf("markers mangled: %q", lines[:2])
	}
	if lines[2] != "note under second" {
		t.Errorf("continuation lost or merged: %q", lines[2])
	}
}

func TestListItemSegments(t *testing.T) {
	item := []string{"- marker text", "cont A", "cont B"}
	got := listItemSegments(item, isUnorderedListItem)
	want := []string{"marker text", "cont A", "cont B"}
	if len(got) != len(want) {
		t.Fatalf("segments = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, got[i], want[i])
		}
	}
	if segs := listItemSegments(nil, isUnorderedListItem); segs != nil {
		t.Errorf("empty item should yield nil, got %q", segs)
	}
}

// Thinking blocks routed through the markdown path must preserve newlines too:
// thinking containing any markdown-ish token (here a backtick) uses the renderer.
func TestThinkingBlock_MarkdownPathPreservesNewlines(t *testing.T) {
	tb := newThinkingBlock("checking `foo` now\nnext thought")
	lines := plainLines(tb.Render(80))
	var content []string
	for _, l := range lines {
		if l == "" || strings.Contains(l, "thinking...") {
			continue
		}
		content = append(content, l)
	}
	joined := strings.Join(content, "\n")
	for _, want := range []string{"checking `foo` now", "next thought"} {
		if !strings.Contains(joined, want) {
			t.Errorf("thinking output missing %q; got:\n%s", want, joined)
		}
	}
	if len(content) < 2 {
		t.Errorf("expected separate visual lines, got: %q", content)
	}
}
