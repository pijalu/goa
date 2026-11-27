// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"fmt"
	"testing"
)

// TestHexToRGB pins the decoder to the former fmt.Sscanf("%02x%02x%02x")
// semantics, including the malformed-input cases, because every caller compares
// rendered strings against golden output.
func TestHexToRGB_MalformedAndCase(t *testing.T) {
	cases := []struct {
		hex  string
		r    uint8
		g    uint8
		b    uint8
		name string
	}{
		{"#ff8800", 0xff, 0x88, 0x00, "rrggbb"},
		{"ff8800", 0xff, 0x88, 0x00, "no hash"},
		{"#f80", 0xff, 0x88, 0x00, "rgb shorthand"},
		{"#FF8800", 0xff, 0x88, 0x00, "uppercase"},
		{"#f80abc", 0xf8, 0x0a, 0xbc, "six digits stay rrggbb"},
		{"#000000", 0, 0, 0, "black"},
		{"#ffffff", 0xff, 0xff, 0xff, "white"},
		{"", 128, 128, 128, "empty falls back to grey"},
		{"#12345", 128, 128, 128, "wrong length"},
		{"#1234567", 128, 128, 128, "too long"},
		{"#zz0000", 0, 0, 0, "invalid first component"},
		{"#ffzz00", 0xff, 0, 0, "invalid second component"},
		{"#ffffzz", 0xff, 0xff, 0, "invalid third component"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b := HexToRGB(tc.hex)
			if r != tc.r || g != tc.g || b != tc.b {
				t.Fatalf("HexToRGB(%q) = (%d,%d,%d), want (%d,%d,%d)", tc.hex, r, g, b, tc.r, tc.g, tc.b)
			}
		})
	}
}

// TestRGBSequences asserts the hand-built escape sequences are byte-identical to
// the fmt.Sprintf form they replaced, across the digit-count boundaries.
func TestRGBSequences(t *testing.T) {
	vals := []uint8{0, 1, 7, 9, 10, 42, 99, 100, 128, 200, 254, 255}
	for _, r := range vals {
		for _, g := range vals {
			for _, b := range []uint8{0, 9, 10, 99, 100, 255} {
				wantFg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
				if got := FgRGB(r, g, b); got != wantFg {
					t.Fatalf("FgRGB(%d,%d,%d) = %q, want %q", r, g, b, got, wantFg)
				}
				wantBg := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
				if got := BgRGB(r, g, b); got != wantBg {
					t.Fatalf("BgRGB(%d,%d,%d) = %q, want %q", r, g, b, got, wantBg)
				}
			}
		}
	}
}

// TestFgBgMemoizedMatchesDirect asserts the memoized path returns exactly what a
// fresh decode would (including malformed hex, which shares the cache key).
func TestFgBgMemoizedMatchesDirect(t *testing.T) {
	hexes := []string{"#ff8800", "#f80", "ff8800", "#zz0000", "", "#12345", "#abcdef"}
	for _, hex := range hexes {
		r, g, b := HexToRGB(hex)
		if got, want := Fg(hex), FgRGB(r, g, b); got != want {
			t.Fatalf("Fg(%q) = %q, want %q", hex, got, want)
		}
		if got, want := Bg(hex), BgRGB(r, g, b); got != want {
			t.Fatalf("Bg(%q) = %q, want %q", hex, got, want)
		}
		// A second call must be served from the cache with the same value.
		if got, want := Fg(hex), FgRGB(r, g, b); got != want {
			t.Fatalf("Fg(%q) cached = %q, want %q", hex, got, want)
		}
	}
}

func BenchmarkFg(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Fg("#ff8800")
	}
}
