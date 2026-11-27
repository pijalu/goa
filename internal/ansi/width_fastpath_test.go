// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

// widthReference is the pre-fast-path implementation: strip every escape with
// the regexp, then sum grapheme-cluster base widths with tab-stop expansion. It
// stays the authority Width must agree with on every input.
func widthReference(s string) int {
	plain := ansiRe.ReplaceAllString(s, "")
	w := 0
	gr := uniseg.NewGraphemes(plain)
	for gr.Next() {
		cluster := gr.Str()
		if strings.ContainsRune(cluster, '\t') {
			w += TabWidth - (w % TabWidth)
			continue
		}
		w += clusterBaseWidth(cluster)
	}
	return w
}

// TestWidth_FastPathMatchesReference pins the ASCII/escape fast path to the
// grapheme-cluster reference for both branches: every fast-path-eligible input
// must produce the same number as the reference, and every ineligible input
// must fall back rather than guess.
func TestWidth_FastPathMatchesReference(t *testing.T) {
	cases := []struct {
		name  string
		input string
		fast  bool // true when asciiWidth is expected to answer
	}{
		{"empty", "", true},
		{"plain ascii", "hello world", true},
		{"ascii with spaces", "  padded  ", true},
		{"sgr wrapped", Fg("#ff0000") + "hi" + Reset, true},
		{"nested sgr", Bold + FgRGB(1, 2, 3) + Underline + "x" + Reset, true},
		{"csi clear", "\x1b[2K\x1b[1;1Habc", true},
		{"osc title", "\x1b]0;title\x07abc", true},
		{"osc st terminated", "\x1b]8;;https://x.test\x1b\\link\x1b]8;;\x1b\\", true},
		{"tab", "a\tb", false},
		{"newline", "a\nb", false},
		{"control byte", "a\x07b", false},
		{"del", "a\x7fb", false},
		{"wide cjk", "日本語", false},
		{"emoji", "👨‍👩‍👧", false},
		{"combining mark", "e\u0301", false},
		{"flag", "🇫🇷", false},
		{"incomplete csi", "abc\x1b[31", false},
		{"lone esc", "abc\x1b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := asciiWidth(tc.input); ok != tc.fast {
				t.Fatalf("asciiWidth(%q) eligible = %v, want %v", tc.input, ok, tc.fast)
			}
			want := widthReference(tc.input)
			if got := Width(tc.input); got != want {
				t.Fatalf("Width(%q) = %d, reference = %d", tc.input, got, want)
			}
		})
	}
}

// TestWidth_FastPathRandomizedCrossChecks compares the fast path against the
// reference over generated mixes of ASCII, escapes and unicode so no escape
// form or byte boundary can silently diverge.
func TestWidth_FastPathRandomizedCrossChecks(t *testing.T) {
	fragments := []string{
		"a", "Z", " ", "~", "!", "\x1b[31m", "\x1b[0m", "\x1b]0;t\x07",
		"\x1b]8;;http://u\x1b\\", "\t", "\n", "日", "é", "👨‍👩‍👧", "🇫🇷",
		"\x1b[1;38;5;200m", "\x1b", "\x1b[", "\x07", "\x7f", "e\u0301",
	}
	// Deterministic pseudo-random walk: no flakiness, full cross-check space.
	seed := uint32(2463534242)
	next := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return int(seed % uint32(n))
	}
	for i := 0; i < 500; i++ {
		var b strings.Builder
		for n := next(6); n > 0; n-- {
			b.WriteString(fragments[next(len(fragments))])
		}
		in := b.String()
		want := widthReference(in)
		if got := Width(in); got != want {
			t.Fatalf("Width(%q) = %d, reference = %d", in, got, want)
		}
	}
}

// TestWidth_ASCIIFastPathAllocations asserts the common terminal-row case does
// not allocate (the streaming hot path measures a row per frame).
func TestWidth_ASCIIFastPathAllocations(t *testing.T) {
	row := Fg("#ff8800") + strings.Repeat("streamed markdown row ", 6) + Reset
	if got := testing.AllocsPerRun(200, func() { Width(row) }); got != 0 {
		t.Fatalf("Width(styled ascii row) allocations = %v, want 0", got)
	}
}

func BenchmarkWidth_ASCII(b *testing.B) {
	row := Fg("#ff8800") + strings.Repeat("streamed markdown row ", 6) + Reset
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Width(row)
	}
}

func BenchmarkWidth_Unicode(b *testing.B) {
	row := Fg("#ff8800") + strings.Repeat("日本語のテキストと絵文字👨‍👩‍👧 ", 4) + Reset
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Width(row)
	}
}
