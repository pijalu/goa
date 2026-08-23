// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "testing"

func TestSGRStateAt(t *testing.T) {
	tests := []struct {
		name string
		row  string
		cut  int
		want string
		ok   bool
	}{
		{
			name: "cut at zero needs nothing",
			row:  "\x1b[38;2;88;166;255mtext\x1b[0m",
			cut:  0,
			want: "",
			ok:   true,
		},
		{
			name: "plain prefix is default pen",
			row:  "plain text only",
			cut:  6,
			want: "",
			ok:   true,
		},
		{
			name: "fg truecolor active at cut",
			row:  "\x1b[38;2;139;148;158m  tree",
			cut:  len("\x1b[38;2;139;148;158m  tr"),
			want: "\x1b[38;2;139;148;158m",
			ok:   true,
		},
		{
			name: "bold plus fg combined restores both",
			row:  " \x1b[1;38;2;88;166;255m## What it i",
			cut:  len(" \x1b[1;38;2;88;166;255m## What it i"),
			want: "\x1b[1;38;2;88;166;255m",
			ok:   true,
		},
		{
			name: "background survives fg reset",
			row:  "\x1b[48;2;42;50;41m \x1b[38;2;139;148;158mTook",
			cut:  len("\x1b[48;2;42;50;41m \x1b[38;2;139;148;158mTo"),
			want: "\x1b[38;2;139;148;158;48;2;42;50;41m",
			ok:   true,
		},
		{
			name: "hard reset clears state back to default",
			row:  "\x1b[31mred\x1b[0mplain",
			cut:  len("\x1b[31mred\x1b[0mpl"),
			want: "",
			ok:   true,
		},
		{
			name: "partial reset 39 keeps background",
			row:  "\x1b[48;5;22m\x1b[38;5;250mtext\x1b[39mpad",
			cut:  len("\x1b[48;5;22m\x1b[38;5;250mtext\x1b[39mpa"),
			want: "\x1b[48;5;22m",
			ok:   true,
		},
		{
			name: "empty params acts as reset",
			row:  "\x1b[31mred\x1b[mplain",
			cut:  len("\x1b[31mred\x1b[mpl"),
			want: "",
			ok:   true,
		},
		{
			name: "non-SGR CSI in prefix is unmodellable",
			row:  "\x1b[2K\x1b[31mred",
			cut:  len("\x1b[2K\x1b[31mre"),
			ok:   false,
		},
		{
			name: "OSC hyperlink in prefix is unmodellable",
			row:  "\x1b]8;;http://x\x07link\x1b]8;;\x07tail",
			cut:  len("\x1b]8;;http://x\x07li"),
			ok:   false,
		},
		{
			name: "colon sub-parameters are unmodellable",
			row:  "\x1b[38:2::1;2;3mtext",
			cut:  len("\x1b[38:2::1;2;3mte"),
			ok:   false,
		},
		{
			name: "lone trailing ESC is unmodellable",
			row:  "text\x1b",
			cut:  len("text\x1b"),
			ok:   false,
		},
		{
			name: "cut splitting a sequence is unmodellable",
			row:  "\x1b[31mred",
			cut:  3, // inside the SGR's parameter bytes
			ok:   false,
		},
		{
			name: "cut beyond the row is rejected",
			row:  "abc",
			cut:  9,
			ok:   false,
		},
		{
			name: "negative cut is rejected",
			row:  "abc",
			cut:  -1,
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SGRStateAt(tt.row, tt.cut)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.ok, got)
			}
			if ok && got != tt.want {
				t.Fatalf("state = %q, want %q", got, tt.want)
			}
		})
	}
}
