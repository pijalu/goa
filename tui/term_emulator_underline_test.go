// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestTermEmulatorSGR58Tolerated(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		wantRow string
	}{
		{
			name:    "bare underline color is invisible",
			stream:  "\x1b[58;2;255;0;0mhello",
			wantRow: "hello",
		},
		{
			name:    "underline color between bg and text",
			stream:  "\x1b[48;2;9;9;9m\x1b[58;2;255;0;0mhi\x1b[0m",
			wantRow: "hi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewTermEmulator(3, 20)
			e.Process(tt.stream)
			if got := e.Visible(0); got != tt.wantRow {
				t.Errorf("Visible(0) = %q, want %q", got, tt.wantRow)
			}
			// Layout integrity: cursor advanced exactly len(text) columns.
			if e.col != len(tt.wantRow) && !strings.HasSuffix(tt.stream, "\x1b[0m") {
				t.Errorf("cursor col = %d, want %d", e.col, len(tt.wantRow))
			}
		})
	}
}

func TestTermEmulatorSGR58DoesNotCorruptBgTracking(t *testing.T) {
	// Regression guard: before SGR 58 was consumed by applySGR, its
	// sub-parameters leaked through as standalone codes — a "0" red channel
	// looked like an SGR 0 reset and silently cleared curBg.
	e := NewTermEmulator(2, 40)
	e.Process("\x1b[48;2;1;2;3mbg\x1b[58;2;0;120;255mul\x1b[38;2;0;200;100mfg\x1b[0mtail")

	bgs := e.VisibleBg(0)
	// "bg", "ul", "fg" (6 cells) keep the background across SGR 58/38
	// specs that contain zero-valued channels; "tail" follows a hard reset
	// and is therefore back at default background.
	for i := 0; i < 6; i++ {
		if bgs[i] != "48;2;1;2;3" {
			t.Fatalf("cell %d bg = %q, want 48;2;1;2;3 preserved after SGR 58/38 with zero channels", i, bgs[i])
		}
	}
	for i := 6; i < len(bgs); i++ {
		if bgs[i] != "" {
			t.Fatalf("cell %d bg = %q, want \"\" after the hard reset", i, bgs[i])
		}
	}
}

func TestTermEmulatorStyledUnderlineColorRender(t *testing.T) {
	// Filmstrip-style render check: a Styled line carrying an underline color,
	// underlined like a markdown link, must replay into the emulator cell-perfect:
	// visible text identical to the plain text and every cell width consumed.
	const w = 30
	line := Styled{hex: "#58a6ff"}.
		WithUnderlineColor("#ff7b00").
		Render("goa docs")

	plain := ansi.Strip(line)
	if plain != "goa docs" {
		t.Fatalf("Strip(Render) = %q, want \"goa docs\"", plain)
	}
	// The SGR 58 prefix must be invisible on the wire: styled width == plain width.
	if got := ansi.Width(line); got != len(plain) {
		t.Fatalf("Width(styled line) = %d, want %d (plain width)", got, len(plain))
	}

	e := NewTermEmulator(2, w)
	e.Process(ansi.Underline + line + ansi.UnderlineReset + "\r\n")
	e.Process("next row")

	if got := e.Visible(0); got != "goa docs" {
		t.Errorf("Visible(0) = %q, want \"goa docs\"", got)
	}
	if got := e.Visible(1); got != "next row" {
		t.Errorf("Visible(1) = %q, want \"next row\"", got)
	}
	if e.col != 8 { // len("next row")
		t.Errorf("final cursor col = %d, want 8", e.col)
	}
}
