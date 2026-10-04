// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// bugs.md B4 — the web UI's input line and bottom status bar must render
// exactly like the terminal's at the same geometry.
//
// The browser *is* the terminal: one engine renders the same compositor bytes
// into a webui.VirtualTerminal (the web grid) and, on a terminal, into a tty.
// This test pins the equivalence where B4 was reported broken: the bottom band
// (input row(s), separator, status row with the right-aligned mode badge, model
// line) must be the same rows, in the same order, at the same geometry — and it
// must all be inside the grid (inside the browser's viewport, because the client
// sizes the grid to the viewport).
//
// Terminal side: the engine renders through a byte-capturing terminal whose
// stream is replayed through tui.TermEmulator — exactly what a real PTY capture
// (e2e/ptydrive --log) yields when replayed. Web side: the same tree on a
// webui.VirtualTerminal, started at the server's default geometry and then
// resized to the browser's, which is what the page does on connect.

// b4Session builds the production startup content plus the footer/status data a
// live session shows, so the bottom band is non-empty. The provider is left
// empty and no activity is set, so the footer renders without time-dependent
// peak/activity markers and the two sides are comparable byte for byte.
func b4Session(t *testing.T, term tui.Terminal, w, h int) *uiScenario {
	t.Helper()
	sc := newUIScenarioTerm(t, term, w, h)
	sc.app.subs.skillRegistry = skills.NewSkillRegistry(nil)
	showStartupBanner(sc.app.subs, sc.chat)
	addStartupInfo(sc.chat, "⟡ Sticky skills (always-on): telegram, thoughtfull")
	addStartupInfo(sc.chat, promptContextBanner("system", nil))
	addStartupInfo(sc.chat, "⟡ Connected to OpenCode Zen Go (deepseek-v4-flash).")
	sc.footer.SetData(tui.FooterData{
		Workdir:       "/home/user/project",
		Mode:          "yolo",
		Model:         "(opencode-go) deepseek-v4-flash",
		Stats:         "↑169k ↓209k  35.8%/1.0M",
		ThinkingLevel: "high",
	})
	sc.editor.SetText("echo B4")
	return sc
}

// b4Render flushes one synchronous frame.
func b4Render(sc *uiScenario) {
	sc.engine.ApplySync(func() {})
	sc.engine.RenderNow()
}

// b4TermScreen is the terminal's own screen at w×h: the engine renders onto a
// byte-capturing terminal and the stream is replayed through TermEmulator.
func b4TermScreen(t *testing.T, w, h int) []string {
	t.Helper()
	tt := &testTerminal{w: w, h: h}
	sc := b4Session(t, tt, w, h)
	b4Render(sc)
	emu := tui.NewTermEmulator(h, w)
	emu.Process(strings.Join(tt.writes, ""))
	rows := make([]string, h)
	for i := range rows {
		rows[i] = strings.TrimRight(emu.CellsText(i), " ")
	}
	return rows
}

// b4WebScreen is the web grid at w×h after the browser's connect flow: the
// session starts at the server default (120×40) and is resized to the client's
// geometry, which repaints the screen at the new size.
func b4WebScreen(t *testing.T, w, h int) []string {
	t.Helper()
	vt := webui.NewVirtualTerminal(webui.DefaultCols, webui.DefaultRows)
	sc := b4Session(t, vt, webui.DefaultCols, webui.DefaultRows)
	vt.Resize(w, h)
	b4Render(sc)
	return strings.Split(vt.Grid().Text(), "\n")
}

// b4Geometries span the browser viewport range: the reported 35/37-row windows
// down to the smallest geometry the client asks for (measureRows clamps to 6).
var b4Geometries = [][2]int{
	{158, 53}, {120, 37}, {117, 35}, {100, 35}, {120, 30}, {100, 24},
	{80, 20}, {60, 12}, {40, 10}, {30, 6},
}

// TestWebUI_BottomBandMatchesTerminal is bugs.md B4: at every geometry the web
// grid must hold exactly the terminal's screen, row for row.
func TestWebUI_BottomBandMatchesTerminal(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	t.Cleanup(func() { tui.SetSpinner(spinner.Definition{}) })

	for _, g := range b4Geometries {
		w, h := g[0], g[1]
		t.Run(geoName(w, h), func(t *testing.T) {
			want := b4TermScreen(t, w, h)
			got := b4WebScreen(t, w, h)
			if len(got) != h {
				t.Fatalf("web grid has %d rows, want %d", len(got), h)
			}
			if len(want) != h {
				t.Fatalf("terminal screen has %d rows, want %d", len(want), h)
			}
			for i := 0; i < h; i++ {
				if got[i] != want[i] {
					t.Errorf("row %d differs from the terminal at %dx%d:\n web: %q\n tty: %q", i, w, h, got[i], want[i])
				}
			}
		})
	}
}

// TestWebUI_BottomBandIsInsideTheGrid states B4's presence half: on a screen
// tall enough for the full chrome, the bottom band is present and ordered
// input row → separator → status row (right-aligned mode) → model line, all
// inside the grid, one row per line. Equality with the terminal is asserted
// above; this pins that equality is not "both missing the band".
func TestWebUI_BottomBandIsInsideTheGrid(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	t.Cleanup(func() { tui.SetSpinner(spinner.Definition{}) })

	const w, h = 120, 30
	rows := b4WebScreen(t, w, h)

	// The model line is the last row, and the status row is directly above it,
	// right-aligned with the upper-cased mode badge on its right edge.
	status, model := rows[h-2], rows[h-1]
	if !strings.Contains(model, "deepseek-v4-flash") {
		t.Errorf("model line is not the last grid row: %q", model)
	}
	if !strings.HasSuffix(status, strings.ToUpper("yolo")) {
		t.Errorf("status row does not end with the right-aligned mode badge: %q", status)
	}
	if !strings.Contains(status, "/home/user/project") {
		t.Errorf("status row lost its left-hand workdir: %q", status)
	}
	// The input line sits between the two separator rules, one row above the
	// status row's blank spacer.
	input := rows[h-4]
	if !strings.Contains(input, "echo B4") {
		t.Errorf("input row above the footer does not hold the typed text: %q", input)
	}
	for _, r := range []int{h - 5, h - 3} {
		if strings.TrimSpace(rows[r]) != strings.Repeat("─", w) {
			t.Errorf("row %d is not a full-width separator: %q", r, rows[r])
		}
	}
}

func geoName(w, h int) string { return fmt.Sprintf("%dx%d", w, h) }