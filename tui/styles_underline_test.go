// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"gopkg.in/yaml.v3"
)

// Round-trip tests: Styled/Theme must emit the ISO 8613-6 underline-color
// sequence (SGR 58) exactly when an underline color is set, and stay
// byte-identical to pre-SGR-58 output when it is not.

func TestStyledUnderlineColor_Emitted(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want string // expected SGR 58 fragment
	}{
		{name: "six-digit hex", hex: "#FF8000", want: "\x1b[58;2;255;128;0m"},
		{name: "three-digit hex", hex: "#0af", want: "\x1b[58;2;0;170;255m"},
		{name: "no hash", hex: "123456", want: "\x1b[58;2;18;52;86m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Styled{hex: "#888888"}.WithUnderlineColor(tt.hex)
			got := s.Render("link")
			if !strings.Contains(got, tt.want) {
				t.Errorf("Render output %q missing SGR 58 fragment %q", got, tt.want)
			}
			// Round-trip: the emitted bytes must parse back into AnsiState.
			var st ansi.AnsiState
			st.Process(tt.want)
			if got := st.GetActiveCodes(); got != tt.want {
				t.Errorf("round-trip: parsed+restored = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStyledUnderlineColor_OmittedWhenUnset(t *testing.T) {
	s := Styled{hex: "#888888"}
	got := s.Render("plain")
	if strings.Contains(got, "58;2") || strings.Contains(got, "59m") {
		t.Errorf("Render without underline color must not emit SGR 58, got %q", got)
	}
	// Byte-exact legacy prefix.
	want := "\x1b[38;2;136;136;136m" + "plain" + "\x1b[0m"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestThemeStyle_PropagatesUnderlineColor(t *testing.T) {
	th := &Theme{Name: "test", Colors: map[string]ColorToken{
		"fancy_link": {Hex: "#00ff00", Bold: true, UnderlineColor: "#123456"},
		"plain":      {Hex: "#abcdef"},
	}}

	prefix := th.Style("fancy_link").Prefix()
	wantFg := "\x1b[38;2;0;255;0m"
	wantUL := "\x1b[58;2;18;52;86m"
	wantBold := "\x1b[1m"
	if !strings.Contains(prefix, wantFg) || !strings.Contains(prefix, wantBold) || !strings.Contains(prefix, wantUL) {
		t.Errorf("Style(fancy_link).Prefix() = %q; want it to contain %q, %q and %q",
			prefix, wantFg, wantBold, wantUL)
	}

	plain := th.Style("plain").Prefix()
	if strings.Contains(plain, "58;2") {
		t.Errorf("token without UnderlineColor must not emit SGR 58, got %q", plain)
	}

	missing := th.Style("nonexistent").Prefix()
	if strings.Contains(missing, "58;2") {
		t.Errorf("fallback style must not emit SGR 58, got %q", missing)
	}
}

func TestColorTokenYAMLUnderlineColor(t *testing.T) {
	// The yaml tag keeps theme files source-compatible: absent keys decode to "".
	var tok ColorToken
	src := "hex: \"#ff0000\"\nunderline_color: \"#00ff00\"\n"
	if err := yaml.Unmarshal([]byte(src), &tok); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tok.UnderlineColor != "#00ff00" {
		t.Errorf("UnderlineColor = %q, want #00ff00", tok.UnderlineColor)
	}
	if tok.Hex != "#ff0000" {
		t.Errorf("Hex = %q, want #ff0000", tok.Hex)
	}
}
