// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// sgrCoalesceFixture is a representative styled transcript canvas: header
// rule, a user bubble (fg+bg), assistant prose built from adjacent same-style
// Styled.Render pieces (the seam pattern that bakes redundant reset+re-open
// pairs), a tool line, italic thinking rows, and chrome footer rows.
func sgrCoalesceFixture() []string {
	gray := TheTheme.Style("toolOutput").Prefix()
	bold := ansi.Bold
	italic := TheTheme.Style("thinking_text").Prefix()
	userFg := ansi.Fg(TheTheme.ColorHex("user_msg"))
	userBg := ansi.Bg(TheTheme.ColorHex("user_msg_bg"))
	headFg := ansi.Fg(TheTheme.ColorHex("heading_fg"))
	okFg := ansi.Fg(TheTheme.ColorHex("tool_success"))
	reset := ansi.Reset

	var rows []string
	rows = append(rows, headFg+"── transcript ──"+reset)
	rows = append(rows, userBg+userFg+"  how do I stream tokens?  "+reset)
	// Assistant prose: three consecutive pieces sharing ONE style — raw
	// emission repeats prefix+reset per piece.
	for _, w := range []string{"You can stream ", "tokens from the ", "provider SDK."} {
		rows = append(rows, gray+"▍ "+reset+gray+w+reset)
	}
	rows = append(rows, bold+okFg+"✔ bash"+reset+bold+" $ go test ./..."+reset)
	for i := 0; i < 8; i++ {
		rows = append(rows, italic+"▌ thinking step "+strings.Repeat("x", 20)+reset)
	}
	// More same-seam prose so the scrollback half carries redundancy too.
	for i := 0; i < 20; i++ {
		rows = append(rows,
			gray+"▍ "+reset+gray+"line "+reset+gray+"of streamed "+reset+gray+"prose."+reset)
	}
	for i := 0; i < 3; i++ {
		rows = append(rows, gray+"footer chrome row "+reset)
	}
	return rows
}

// renderSGRFrames drives comp through a full-repaint frame of canvas (and
// returns nothing; writes accumulate on term).
func renderSGRFrame(comp *Compositor, term *fakeTerminal, canvas []string, width, height int) {
	comp.drawWindow(canvas, nil, width, height, false)
}

func totalWritten(term *fakeTerminal) int {
	total := 0
	for _, w := range term.Writes() {
		total += len(w)
	}
	return total
}

// wantMinReductionPct is the explicit reduction floor asserted by the
// byte-count test below. Measured on sgrCoalesceFixture at 80×24: 44%
// (6452 → 3597 bytes for a full-repaint frame); the floor keeps margin for
// theme/fixture drift while still pinning a substantial win.
const wantMinReductionPct = 30

// TestCompositor_SGRCoalesce_ReducesBytes renders a representative styled
// transcript through the compositor's row-emission path with the coalescer
// enabled and disabled, and asserts an explicit measured byte reduction.
func TestCompositor_SGRCoalesce_ReducesBytes(t *testing.T) {
	const width, height = 80, 24
	canvas := sgrCoalesceFixture()

	rawTerm := &fakeTerminal{w: width, h: height}
	rawComp := NewCompositor(rawTerm)
	rawComp.sgr = nil // baseline: unfiltered emission
	renderSGRFrame(rawComp, rawTerm, canvas, width, height)

	coalTerm := &fakeTerminal{w: width, h: height}
	coalComp := NewCompositor(coalTerm)
	renderSGRFrame(coalComp, coalTerm, canvas, width, height)

	raw, coal := totalWritten(rawTerm), totalWritten(coalTerm)
	if coal >= raw {
		t.Fatalf("coalesced output not smaller: raw=%d coalesced=%d", raw, coal)
	}
	reductionPct := 100 * (raw - coal) / raw
	t.Logf("frame bytes: raw=%d coalesced=%d reduction=%.1f%%", raw, coal, float64(reductionPct))
	if reductionPct < wantMinReductionPct {
		t.Errorf("reduction %d%% < required %d%% (raw=%d coalesced=%d)",
			reductionPct, wantMinReductionPct, raw, coal)
	}

	// The seam pattern must actually collapse: no reset immediately followed
	// by an identical truecolor reopen anywhere in the coalesced stream.
	stream := strings.Join(coalTerm.Writes(), "")
	seam := ansi.Reset + TheTheme.Style("toolOutput").Prefix()
	if strings.Contains(stream, seam) {
		t.Errorf("coalesced stream still contains reset+same-prefix seam %q", seam)
	}
}

// TestCompositor_SGRCoalesce_ScreenEquivalent pins correctness: replaying the
// raw and the coalesced wire streams from the SAME input frames through a
// TermEmulator must yield identical visible rows (foreground AND background
// cells); a screenEmulator additionally confirms identical scrollback and
// hardware cursor.
func TestCompositor_SGRCoalesce_ScreenEquivalent(t *testing.T) {
	const width, height = 80, 24

	render := func(enabled bool) []string {
		canvas := sgrCoalesceFixture()
		term := &fakeTerminal{w: width, h: height}
		comp := NewCompositor(term)
		if !enabled {
			comp.sgr = nil
		}
		renderSGRFrame(comp, term, canvas, width, height)
		// A second frame grows the canvas: exercises the incremental diff
		// path (emitRowUpdate partials + unchanged-row skips) too.
		grown := append(append([]string{}, canvas...),
			TheTheme.Style("toolOutput").Prefix()+"appended "+TheTheme.Style("toolOutput").Prefix()+"styled tail"+ansi.Reset)
		grown = append(grown, grown[len(grown)-4]) // another styled row
		comp.prevLines = copySlice(canvas)
		comp.renderDiff(grown, nil, width, height)
		return term.Writes()
	}

	rawWrites := render(false)
	coalWrites := render(true)

	// TermEmulator identity: every visible row's fg text and bg cell colors.
	rawEmu, coalEmu := NewTermEmulator(height, width), NewTermEmulator(height, width)
	for _, w := range rawWrites {
		rawEmu.Process(w)
	}
	for _, w := range coalWrites {
		coalEmu.Process(w)
	}
	for row := 0; row < height; row++ {
		if got, want := coalEmu.Visible(row), rawEmu.Visible(row); got != want {
			t.Errorf("TermEmulator row %d diverged:\nraw:  %q\ncoal: %q", row, want, got)
		}
		if got, want := coalEmu.VisibleBg(row), rawEmu.VisibleBg(row); !strSliceEqual(got, want) {
			t.Errorf("TermEmulator bg cells row %d diverged:\nraw:  %q\ncoal: %q", row, want, got)
		}
	}

	// screenEmulator identity: scrollback content and hardware cursor.
	rawSnap := replayScreenEmulator(height, width, rawWrites)
	coalSnap := replayScreenEmulator(height, width, coalWrites)
	if strings.Join(rawSnap.screen, "\x00") != strings.Join(coalSnap.screen, "\x00") {
		t.Errorf("visible screen diverged:\nraw:\n%s\n\ncoalesced:\n%s",
			strings.Join(rawSnap.screen, "\n"), strings.Join(coalSnap.screen, "\n"))
	}
	if strings.Join(rawSnap.scrollback, "\x00") != strings.Join(coalSnap.scrollback, "\x00") {
		t.Errorf("scrollback diverged:\nraw:\n%s\n\ncoalesced:\n%s",
			strings.Join(rawSnap.scrollback, "\n"), strings.Join(coalSnap.scrollback, "\n"))
	}
	if rawSnap.row != coalSnap.row || rawSnap.col != coalSnap.col {
		t.Errorf("cursor diverged: raw=(%d,%d) coalesced=(%d,%d)", rawSnap.row, rawSnap.col, coalSnap.row, coalSnap.col)
	}
}

type sgrScreenSnapshot struct {
	screen     []string
	scrollback []string
	row, col   int
}

func replayScreenEmulator(h, w int, writes []string) sgrScreenSnapshot {
	emu := newScreenEmulator(h, w)
	for _, wr := range writes {
		emu.Process(wr)
	}
	screen := make([]string, len(emu.screen))
	copy(screen, emu.screen)
	sb := make([]string, len(emu.scrollback))
	copy(sb, emu.scrollback)
	return sgrScreenSnapshot{screen: screen, scrollback: sb, row: emu.row, col: emu.col}
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
