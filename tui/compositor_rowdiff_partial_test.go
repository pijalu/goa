// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

const rdR = "\x1b[0m" // trailing reset appended by applyLineResets

// TestPlanPartialRow_PartialRanges covers rows whose dirty range excludes an
// edge: prefix-stable rows (tail mode), suffix-stable rows (head mode), and a
// middle-only change where both modes are viable and the cheaper one must win.
func TestPlanPartialRow_PartialRanges(t *testing.T) {
	tests := []struct {
		name string
		prev string
		cur  string
		want colRangeWant
	}{
		{
			name: "tail append after stable prefix",
			prev: "hello" + rdR,
			cur:  "hello world" + rdR,
			want: colRangeWant{partial: true, col: 5, seg: " world" + rdR},
		},
		{
			name: "tail letter change near end rewrites through row end",
			prev: "doing thing-A now" + rdR,
			cur:  "doing thing-B now" + rdR,
			want: colRangeWant{partial: true, col: 12, seg: "B now" + rdR},
		},
		{
			name: "head-stable braille spinner tick",
			prev: "\u280b Working" + rdR,
			cur:  "\u2819 Working" + rdR,
			want: colRangeWant{partial: true, col: 0, seg: "\u2819"},
		},
		{
			name: "head-stable flag emoji swap",
			prev: "\U0001f1fa\U0001f1f8 ok" + rdR,
			cur:  "\U0001f1fa\U0001f1eb ok" + rdR,
			want: colRangeWant{partial: true, col: 0, seg: "\U0001f1fa\U0001f1eb"},
		},
		{
			name: "head-stable combining mark recomposes the cluster",
			prev: "cafe\u0301 ok" + rdR,
			cur:  "cafe\u0302 ok" + rdR,
			want: colRangeWant{partial: true, col: 0, seg: "cafe\u0302"},
		},
		{
			name: "middle-only change picks cheaper head plan",
			prev: "total: 100 items" + rdR,
			cur:  "total: 200 items" + rdR,
			want: colRangeWant{partial: true, col: 0, seg: "total: 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planPartialRow(tt.prev, tt.cur, 80)
			assertColRange(t, got, tt.want)
		})
	}
}

type colRangeWant struct {
	partial bool
	col     int
	seg     string
}

func assertColRange(t *testing.T, got rowUpdate, want colRangeWant) {
	t.Helper()
	if got.partial != want.partial {
		t.Fatalf("partial = %v, want %v (plan: %+v)", got.partial, want.partial, got)
	}
	if !want.partial {
		return
	}
	if got.col != want.col || got.seg != want.seg {
		t.Fatalf("plan = {col:%d seg:%q}, want {col:%d seg:%q}", got.col, got.seg, want.col, want.seg)
	}
}

// TestPlanTailRow_RejectsBareCombiningMark exercises the startsPrintable guard
// directly: a tail whose FIRST visible cluster is a bare combining mark cannot
// repaint its cell in isolation and must fall back.
func TestPlanTailRow_RejectsBareCombiningMark(t *testing.T) {
	prev := "ab x" + rdR      // stable prefix "ab "
	cur := "ab \u0301x" + rdR // tail begins with a bare combining mark
	p := commonPrefixLen(prev, cur)
	if p != 3 {
		t.Fatalf("prefix = %d, want 3", p)
	}
	if up, ok := planTailRow(prev, cur, 40, p); ok {
		t.Fatalf("bare-combining-mark tail accepted: %+v", up)
	}
}

// TestPlanPartialRow_FallbacksToFullRow covers every mandated fallback: dirty
// boundaries that would split an escape sequence or a wide grapheme, plus the
// precondition rejections (controls, OSC, unclosed styling, width shrink).
func TestPlanPartialRow_FallbacksToFullRow(t *testing.T) {
	tests := []struct {
		name string
		prev string
		cur  string
	}{
		{
			name: "color-code change splits escape sequence",
			prev: "\x1b[31mabc" + rdR,
			cur:  "\x1b[32mabc" + rdR,
		},
		{
			name: "flag emojis differ only mid-cluster with no stable edge",
			prev: "\U0001f1fa\U0001f1f8",
			cur:  "\U0001f1fa\U0001f1eb",
		},
		{
			name: "thumbs emoji swap has no stable edge",
			prev: "\U0001f44d",
			cur:  "\U0001f44e",
		},
		{
			name: "tab-containing rows fall back",
			prev: "a\tb" + rdR,
			cur:  "a\tc" + rdR,
		},
		{
			name: "unclosed SGR styling falls back",
			prev: "\x1b[31mred tail",
			cur:  "\x1b[32mblue tail",
		},
		{
			name: "OSC hyperlink rows fall back",
			prev: "\x1b]8;;http://x\x07link\x1b]8;;\x07" + rdR,
			cur:  "\x1b]8;;http://y\x07link\x1b]8;;\x07" + rdR,
		},
		{
			name: "empty previous row needs full clear",
			prev: "",
			cur:  "fresh" + rdR,
		},
		{
			name: "row deletion at end shrinks tail",
			prev: "abcdef" + rdR,
			cur:  "abcde" + rdR,
		},
		{
			name: "identical rows never plan partial",
			prev: "same" + rdR,
			cur:  "same" + rdR,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planPartialRow(tt.prev, tt.cur, 80)
			if got.partial {
				t.Fatalf("expected full-row fallback, got partial {col:%d seg:%q}", got.col, got.seg)
			}
		})
	}
}

// TestEmitRowUpdate_Formats verifies the exact wire format of both emission
// modes: partial = CUP(row;col+1) + segment, full = CUP(row;1) + EL2 + line.
func TestEmitRowUpdate_Formats(t *testing.T) {
	c := &Compositor{}

	var b strings.Builder
	c.emitRowUpdate(&b, 7, "hello"+rdR, "hello world"+rdR, 80)
	if got, want := b.String(), "\x1b[7;6H world"+rdR; got != want {
		t.Fatalf("tail emit = %q, want %q", got, want)
	}

	b.Reset()
	c.emitRowUpdate(&b, 3, "", "brand new"+rdR, 80)
	if got, want := b.String(), "\x1b[3;1H\x1b[2Kbrand new"+rdR; got != want {
		t.Fatalf("full emit = %q, want %q", got, want)
	}
}
