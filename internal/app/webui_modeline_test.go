// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

func b26ModeLineSession(t *testing.T, term tui.Terminal, w, h int) *uiScenario {
	t.Helper()
	sc := b4Session(t, term, w, h)
	sc.footer.SetModeLine(
		[]string{"app-badge", "mode-badge", "current-file"},
		[]string{"token-bar", "activity", "help-hint"},
	)
	return sc
}

func b26TermScreen(t *testing.T, w, h int) []string {
	t.Helper()
	term := &testTerminal{w: w, h: h}
	sc := b26ModeLineSession(t, term, w, h)
	b4Render(sc)
	emu := tui.NewTermEmulator(h, w)
	emu.Process(strings.Join(term.writes, ""))
	rows := make([]string, h)
	for row := range rows {
		rows[row] = strings.TrimRight(emu.CellsText(row), " ")
	}
	return rows
}

func b26WebScreen(t *testing.T, w, h int) []string {
	t.Helper()
	vt := webui.NewVirtualTerminal(webui.DefaultCols, webui.DefaultRows)
	sc := b26ModeLineSession(t, vt, webui.DefaultCols, webui.DefaultRows)
	vt.Resize(w, h)
	b4Render(sc)
	return strings.Split(vt.Grid().Text(), "\n")
}

// TestWebUI_ModeLineMatchesTerminalCells is the B26 renderer-parity pin. The
// browser receives the authoritative cell grid; comparing it with the terminal
// emulator's replay proves that WEB follows the configured TUI modeline rather
// than maintaining a second status-bar implementation.
func TestWebUI_ModeLineMatchesTerminalCells(t *testing.T) {
	for _, geometry := range [][2]int{{120, 30}, {60, 12}, {40, 10}} {
		w, h := geometry[0], geometry[1]
		t.Run(geoName(w, h), func(t *testing.T) { assertModeLineCellParity(t, w, h) })
	}
}

func assertModeLineCellParity(t *testing.T, w, h int) {
	t.Helper()
	want := b26TermScreen(t, w, h)
	got := b26WebScreen(t, w, h)
	if len(got) != len(want) {
		t.Fatalf("web rows = %d, terminal rows = %d", len(got), len(want))
	}
	for row := range want {
		if got[row] != want[row] {
			t.Errorf("row %d differs:\n web: %q\n tty: %q", row, got[row], want[row])
		}
	}
	if w == 120 {
		assertModeLineContainsAll(t, strings.Join(want, "\n"), []string{"⬡ goa", "/help", "↑169k", "deepseek-v4-flash"})
	}
}

func assertModeLineContainsAll(t *testing.T, text string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("configured terminal band lacks %q:\n%s", want, text)
		}
	}
}
