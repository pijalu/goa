// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// rdRenderPair renders two frames on a 60x24 terminal and returns the first
// frame's writes plus the second frame's diff writes.
func rdRenderPair(t *testing.T, first, second []string, chrome int) (firstWrites, diff []string) {
	t.Helper()
	const w, h = 60, 24
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)
	mk := func(rows []string) *Scene {
		return &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chrome,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(rows)}, Content: rows}}}
	}
	comp.Render(mk(first))
	fw := term.Writes()
	term.writes = nil
	comp.Render(mk(second))
	return fw, term.Writes()
}

// rdEmulate replays all writes through a fresh TermEmulator.
func rdEmulate(writes []string) *TermEmulator {
	emu := NewTermEmulator(24, 60)
	for _, wr := range writes {
		emu.Process(wr)
	}
	return emu
}

// TestCompositor_UnchangedRowsSkippedPreserved pins the unchanged-row skip on
// the diff path: rendering an identical frame after the first must emit no row
// updates at all — only the sync envelope.
func TestCompositor_UnchangedRowsSkippedPreserved(t *testing.T) {
	// The canvas must fill the window: blank rows below contentEnd are
	// (pre-existing behavior) re-cleared every diff frame.
	rows := make([]string, 24)
	for i := range rows {
		rows[i] = "stable-" + itoaStr(i)
	}
	_, diff := rdRenderPair(t, rows, rows, 0)
	joined := strings.Join(diff, "")
	if joined != "\x1b[?2026h\x1b[?2026l" {
		t.Fatalf("identical frame emitted more than the sync envelope: %q", joined)
	}
}

// TestCompositor_ChangedRowEmitsPartialUpdate drives the compositor through a
// mid-line churn frame and asserts the changed transcript row is emitted as a
// column-range update while every other visible row stays untouched.
func TestCompositor_ChangedRowEmitsPartialUpdate(t *testing.T) {
	first := []string{"header-one", "header-two", "processing file-alpha.tar.gz now", "footer-one", "footer-two"}
	second := []string{"header-one", "header-two", "processing file-omega.tar.gz now", "footer-one", "footer-two"}
	fw, diff := rdRenderPair(t, first, second, 0)
	d := strings.Join(diff, "")

	// Screen row 3 holds the changed canvas row. The stable prefix
	// "processing file-" is 16 columns, so the update starts at col 17.
	if !strings.Contains(d, "\x1b[3;17H") {
		t.Fatalf("changed row not emitted as column-range update:\n%q", d)
	}
	if strings.Contains(d, "\x1b[3;1H\x1b[2K") {
		t.Fatalf("changed row was fully cleared instead of partially updated:\n%q", d)
	}
	for _, row := range []int{1, 2, 4, 5} {
		if strings.Contains(d, "\x1b["+itoaStr(row)+";") {
			t.Fatalf("unchanged screen row %d was touched by the diff:\n%q", row, d)
		}
	}

	emu := rdEmulate(append(append([]string{}, fw...), diff...))
	if got := emu.Visible(2); !strings.Contains(got, "file-omega.tar.gz") {
		t.Fatalf("emulated row 3 shows %q, want updated file name", got)
	}
	if got, want := emu.Visible(0), "header-one"; !strings.Contains(got, want) {
		t.Fatalf("emulated row 1 = %q, want it to still contain %q", got, want)
	}
}

// TestCompositor_RowDiffFallbacksFullEmit drives escape-sequence-spanning and
// wide-grapheme churn through the compositor and asserts those rows take the
// legacy full-row path — plus that the emulator still ends up correct.
func TestCompositor_RowDiffFallbacksFullEmit(t *testing.T) {
	// The color change splits an escape sequence; the emoji→narrow swap is a
	// width-changing wide grapheme with no usable stable edge. Both must take
	// the legacy full-row clear+rewrite path.
	first := []string{"prefix", "\x1b[31mabc" + rdR, "suffix", "\U0001f44d"}
	second := []string{"prefix", "\x1b[32mabc" + rdR, "suffix", "x"}
	fw, diff := rdRenderPair(t, first, second, 0)
	d := strings.Join(diff, "")

	// Both changed rows must take the legacy full-row clear+rewrite path:
	// the color change splits an escape sequence; the emoji swap has no
	// stable edge around the wide grapheme.
	if !strings.Contains(d, "\x1b[2;1H\x1b[2K") {
		t.Fatalf("escape-spanning change did not fall back to full-row emit:\n%q", d)
	}
	if !strings.Contains(d, "\x1b[4;1H\x1b[2K") {
		t.Fatalf("wide-grapheme change without stable edge did not fall back:\n%q", d)
	}

	emu := rdEmulate(append(append([]string{}, fw...), diff...))
	if got, want := ansi.Strip(emu.Visible(3)), "x"; got != want {
		t.Fatalf("emulated emoji row = %q, want %q", got, want)
	}
	if got, want := ansi.Strip(emu.Visible(1)), "abc"; !strings.Contains(got, want) {
		t.Fatalf("emulated styled row = %q, want it to show %q", got, want)
	}
}

// TestCompositor_ChromePathParity proves the chrome band uses the same
// column-range mechanism as the transcript: an editor-line churn emits a
// partial CUP into the pinned bottom row with no erase, and the identical
// byte-level pair planned through the shared planner produces exactly the
// segment the chrome path emitted.
func TestCompositor_ChromePathParity(t *testing.T) {
	const w, h = 60, 24
	const chrome = 2
	mkRows := func(cmd string) []string {
		rows := make([]string, 0, 10)
		for i := 0; i < 8; i++ {
			rows = append(rows, "log-"+itoaStr(i))
		}
		return append(rows, "EDITOR-BORDER", "> "+cmd)
	}
	first, second := mkRows("build-gamma"), mkRows("build-delta")
	fw, diff := rdRenderPair(t, first, second, chrome)
	d := strings.Join(diff, "")
	all := append(append([]string{}, fw...), diff...)

	// The editor input line is the LAST screen row; the shared prefix
	// "> build-" is 8 columns → partial CUP at col 9.
	if !strings.Contains(d, "\x1b["+itoaStr(h)+";9H") {
		t.Fatalf("chrome editor row not emitted as partial update:\n%q", d)
	}
	if strings.Contains(d, "\x1b["+itoaStr(h)+";1H\x1b[2K") {
		t.Fatalf("chrome editor row was fully cleared:\n%q", d)
	}

	// Parity: planning the same pair through the shared entry point yields
	// the exact plan the chrome path just emitted.
	up := planPartialRow("> build-gamma"+rdR, "> build-delta"+rdR, w)
	if !up.partial || up.col != 8 {
		t.Fatalf("shared planner disagrees with chrome emission: %+v", up)
	}
	// Emit-time SGR coalescing elides the segment's trailing reset when the
	// terminal is already at default attributes (this fixture styles
	// nothing), so parity is asserted on the state-visible part of the
	// segment; the emulator replay below pins full rendering equivalence.
	wantSeg := strings.TrimSuffix(up.seg, ansi.Reset)
	if !strings.Contains(d, "\x1b["+itoaStr(h)+";"+itoaStr(up.col+1)+"H"+wantSeg) {
		t.Fatalf("chrome emission does not match transcript-path plan %+v:\n%q", up, d)
	}

	emu := rdEmulate(all)
	if got := emu.Visible(h - 1); !strings.Contains(got, "> build-delta") {
		t.Fatalf("emulated editor row = %q, want updated command", got)
	}
	// The transcript above the band must be untouched by the churn.
	for r := 0; r < 8; r++ {
		if got, want := emu.Visible(r), "log-"+itoaStr(r); !strings.Contains(got, want) {
			t.Fatalf("emulated transcript row %d = %q, want %q", r+1, got, want)
		}
	}
}
