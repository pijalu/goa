// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"testing"

	"github.com/rivo/uniseg"
)

func TestEscapeSpans(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []ByteSpan
	}{
		{"plain text", "hello", nil},
		{"empty", "", nil},
		{
			"single CSI",
			"a\x1b[31mb",
			[]ByteSpan{{1, 6}},
		},
		{
			"two CSIs",
			"\x1b[1mhi\x1b[0m!",
			[]ByteSpan{{0, 4}, {6, 10}},
		},
		{
			"osc bel",
			"x\x1b]8;;http://e\x07y",
			[]ByteSpan{{1, 15}},
		},
		{
			"osc st",
			"\x1b]8;;u\x1b\\z",
			[]ByteSpan{{0, 8}},
		},
		{
			"truncated csi counts to end",
			"ab\x1b[3",
			[]ByteSpan{{2, 5}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeSpans(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("EscapeSpans(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("EscapeSpans(%q)[%d] = %v, want %v", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestInEscapeSpan(t *testing.T) {
	s := "a\x1b[31mbc"
	cases := []struct {
		o    int
		want bool
	}{
		{0, false}, // 'a'
		{1, false}, // ESC start (not strictly inside)
		{2, true},  // '['
		{3, true},  // '3'
		{5, true},  // final byte 'm', still inside [1,6)
		{6, false}, // 'b'
		{7, false}, // 'c'
	}
	for _, c := range cases {
		if got := InEscapeSpan(s, c.o); got != c.want {
			t.Errorf("InEscapeSpan(%q, %d) = %v, want %v", s, c.o, got, c.want)
		}
	}
}

func TestSafeCut(t *testing.T) {
	tests := []struct {
		name string
		s    string
		o    int
		want bool
	}{
		{"offset 0 trivially safe", "abc", 0, true},
		{"offset len trivially safe", "abc", 3, true},
		{"out of range high", "abc", 9, true},
		{"negative", "abc", -1, true},
		{"plain middle", "abcdef", 3, true},
		{
			"inside csi sequence",
			"a\x1b[31mb",
			3,
			false,
		},
		{
			"at csi start boundary",
			"a\x1b[31mb",
			1,
			true,
		},
		{
			"after csi end boundary",
			"a\x1b[31mb",
			6,
			true,
		},
		{
			"mid combining cluster: e + acute",
			"éclair",
			1,
			false,
		},
		{
			"after combining cluster",
			"éclair",
			3,
			true,
		},
		{
			"between flag regional indicators",
			"\U0001f1fa\U0001f1f8 ok",
			4,
			false,
		},
		{
			"after full flag",
			"\U0001f1fa\U0001f1f8 ok",
			8,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeCut(tt.s, tt.o); got != tt.want {
				t.Errorf("SafeCut(%q, %d) = %v, want %v", tt.s, tt.o, got, tt.want)
			}
		})
	}
}

// TestSafeCutNeverSplitsCluster verifies exhaustively over a ZWJ emoji family
// that every SafeCut-approved offset is a real cluster boundary.
func TestSafeCutNeverSplitsCluster(t *testing.T) {
	s := "\U0001f468\u200d\U0001f469\u200d\U0001f467x" // man+ZWJ+woman+ZWJ+girl, then 'x'
	var bounds []int
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		start, end := gr.Positions()
		bounds = append(bounds, start, end)
	}
	for o := 0; o <= len(s); o++ {
		safe := SafeCut(s, o)
		isBoundary := false
		for _, b := range bounds {
			if b == o {
				isBoundary = true
				break
			}
		}
		if safe && !isBoundary {
			t.Fatalf("SafeCut(%q, %d)=true but %d is not a cluster boundary (bounds=%v)", s, o, o, bounds)
		}
	}
	// The ZWJ cluster must be indivisible: interior offsets are unsafe.
	if SafeCut(s, 4) {
		t.Fatalf("SafeCut allowed cutting inside the ZWJ family emoji")
	}
}
