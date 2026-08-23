// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"strings"
	"testing"
)

// The SGR 58 (ISO 8613-6 underline color) contract:
//   - Strip removes the sequence entirely,
//   - Width counts it as zero-width,
//   - AnsiState parses it (and its 59 reset) so state restoration and SGR
//     coalescing stay exact,
//   - TermEmulator-side tolerance lives in tui (see term_emulator tests).

func TestStripRemovesUnderlineColor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "truecolor underline color",
			in:   "\x1b[58;2;10;20;30mfoo\x1b[0m",
			want: "foo",
		},
		{
			name: "256-color underline color with 59 reset",
			in:   "\x1b[58;5;196mfoo\x1b[59mbar",
			want: "foobar",
		},
		{
			name: "mixed into a styled run",
			in:   "\x1b[4m\x1b[38;2;1;2;3m\x1b[58;2;255;128;0mlink\x1b[24m\x1b[39m\x1b[59m",
			want: "link",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Strip(tt.in); got != tt.want {
				t.Errorf("Strip(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWidthUnderlineColorZeroWidth(t *testing.T) {
	const ul = "\x1b[58;2;10;20;30m"
	if got := Width(ul + "abc"); got != 3 {
		t.Errorf("Width(SGR58+\"abc\") = %d, want 3", got)
	}
	if got := Width("ab" + ul + "\x1b[4mcd"); got != 4 {
		t.Errorf("Width(\"ab\"+SGR58+underline+\"cd\") = %d, want 4", got)
	}
	if got := Width(ul + "\x1b[59m"); got != 0 {
		t.Errorf("Width(bare SGR 58/59) = %d, want 0", got)
	}
}

func TestAnsiStateParsesUnderlineColor(t *testing.T) {
	var st AnsiState
	st.Process("\x1b[58;2;10;20;30m")
	if got := st.GetActiveCodes(); got != "\x1b[58;2;10;20;30m" {
		t.Errorf("GetActiveCodes after SGR 58 = %q, want %q", got, "\x1b[58;2;10;20;30m")
	}

	// Combined run restores both underline and its color.
	var combined AnsiState
	combined.Process("\x1b[4;58;5;196m")
	if got := combined.GetActiveCodes(); got != "\x1b[4;58;5;196m" {
		t.Errorf("GetActiveCodes after 4;58;5;196 = %q, want %q", got, "\x1b[4;58;5;196m")
	}

	// 59 resets only the underline color.
	combined.Process("\x1b[59m")
	if got := combined.GetActiveCodes(); got != "\x1b[4m" {
		t.Errorf("GetActiveCodes after 59 = %q, want \"\\x1b[4m\"", got)
	}

	// Hard reset clears everything.
	st.Process("\x1b[0m")
	if got := st.GetActiveCodes(); got != "" {
		t.Errorf("GetActiveCodes after reset = %q, want empty", got)
	}
	if !st.isDefaultSGR() {
		t.Error("state should be default after reset")
	}
}

func TestAnsiStateEqualSGRUnderlineColor(t *testing.T) {
	var a, b AnsiState
	a.Process("\x1b[58;2;1;2;3m")
	if a.EqualSGR(&b) {
		t.Error("states differing only in underline color must not be EqualSGR")
	}
	b.Process("\x1b[58;2;1;2;3m")
	if !a.EqualSGR(&b) {
		t.Error("identical states (incl. underline color) should be EqualSGR")
	}
}

func TestSGRCoalescerUnderlineColor(t *testing.T) {
	t.Run("duplicate 58 prefix elided without poisoning", func(t *testing.T) {
		sc := NewSGRCoalescer()
		first := sc.Filter("\x1b[58;2;9;9;9mhello")
		if first != "\x1b[58;2;9;9;9mhello" {
			t.Errorf("first frame = %q, want prefix kept verbatim", first)
		}
		second := sc.Filter("\x1b[58;2;9;9;9mworld")
		if second != "world" {
			t.Errorf("second frame = %q, want duplicate 58 prefix elided to \"world\"", second)
		}
	})

	t.Run("changed 58 canonicalized", func(t *testing.T) {
		sc := NewSGRCoalescer()
		sc.Filter("\x1b[58;2;9;9;9ma")
		got := sc.Filter("\x1b[58;2;1;2;3mb")
		want := "\x1b[58;2;1;2;3mb"
		if got != want {
			t.Errorf("second frame = %q, want %q", got, want)
		}
	})

	t.Run("58 survives reset re-open merge", func(t *testing.T) {
		// reset followed by re-open of the tracked state collapses entirely;
		// underline color participates in the tracked state.
		sc := NewSGRCoalescer()
		sc.Filter("\x1b[58;2;9;9;9mx")
		got := sc.Filter("\x1b[0m\x1b[58;2;9;9;9my")
		if got != "y" {
			t.Errorf("reset+re-open collapsed to %q, want \"y\"", got)
		}
	})

	t.Run("58 alongside fg/bg stays fully modeled", func(t *testing.T) {
		sc := NewSGRCoalescer()
		sc.Filter("\x1b[38;2;0;0;1;48;2;0;0;2;58;2;0;0;3mfirst")
		got := sc.Filter("\x1b[38;2;0;0;1;48;2;0;0;2;58;2;0;0;3msecond")
		if got != "second" {
			t.Errorf("identical triple-color run elided to %q, want \"second\"", got)
		}
	})
}

func TestUnderlineColorSequences(t *testing.T) {
	if got, want := UnderlineColor("#FF8000"), "\x1b[58;2;255;128;0m"; got != want {
		t.Errorf("UnderlineColor(#FF8000) = %q, want %q", got, want)
	}
	if got, want := UnderlineColorRGB(0, 16, 255), "\x1b[58;2;0;16;255m"; got != want {
		t.Errorf("UnderlineColorRGB(0,16,255) = %q, want %q", got, want)
	}
	if got := UnderlineColorReset; got != CSI+"59m" || !strings.HasPrefix(got, "\x1b[") {
		t.Errorf("UnderlineColorReset = %q, want ESC[59m", got)
	}
}
