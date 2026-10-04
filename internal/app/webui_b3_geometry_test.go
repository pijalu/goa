// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/skills"
)

// b3Sink records the transcript rows frames ship to a browser: the rows that
// scrolled off the top of the screen. B3 is about a *geometry change* creating
// such rows out of the screen it replaced, so the sink is the assertion surface
// for "no row of the current screen may be shipped as history".
type b3Sink struct {
	grid   *webui.CellGrid
	frames []b3Frame
}

// b3Frame is one published frame: the transcript rows it shipped and the rows
// the screen held at that moment.
type b3Frame struct {
	rows   []string
	screen []string
}

func (s *b3Sink) attach(vt *webui.VirtualTerminal) { s.grid = vt.Grid() }

func (s *b3Sink) Publish(f *webui.Frame) {
	var fr b3Frame
	for _, p := range f.Scrollback {
		fr.rows = append(fr.rows, strings.TrimRight(webui.RunsText(p.Runs), " "))
	}
	if s.grid != nil {
		for _, r := range strings.Split(s.grid.Text(), "\n") {
			fr.screen = append(fr.screen, strings.TrimRight(r, " "))
		}
	}
	s.frames = append(s.frames, fr)
}

func (s *b3Sink) HasClients() bool { return true }

// shipped returns every transcript row the sink saw, in frame order.
func (s *b3Sink) shipped() []string {
	var out []string
	for _, fr := range s.frames {
		out = append(out, fr.rows...)
	}
	return out
}

// newB3Session builds a live web session at cols×rows with the production
// startup banner already rendered — the state a browser attaches to.
func newB3Session(t *testing.T, cols, rows int) (*webui.VirtualTerminal, *uiScenario, *b3Sink) {
	t.Helper()
	vt := webui.NewVirtualTerminal(cols, rows)
	sink := &b3Sink{}
	sink.attach(vt)
	vt.SetSink(sink)
	sc := newUIScenarioTerm(t, vt, cols, rows)
	sc.app.subs.skillRegistry = skills.NewSkillRegistry(nil)
	showStartupBanner(sc.app.subs, sc.chat)
	b3Render(sc)
	return vt, sc, sink
}

func b3Render(sc *uiScenario) {
	sc.engine.ApplySync(func() {})
	sc.engine.RenderNow()
}

func b3Rows(vt *webui.VirtualTerminal) []string { return strings.Split(vt.Grid().Text(), "\n") }

// b3HeaderComplete reports whether the grid shows the mascot from its first
// row (a whole startup screen), i.e. the screen has not been scrolled.
func b3HeaderComplete(t *testing.T, vt *webui.VirtualTerminal) bool {
	t.Helper()
	rows := b3Rows(vt)
	if len(rows) == 0 {
		return false
	}
	// The mascot's first row is the widest art row; a scrolled screen starts
	// mid-art (a narrower fragment preceded by the lost rows in history).
	return strings.HasPrefix(strings.TrimRight(rows[0], " "), "          ▄▄▄▄▄")
}

// TestWebUI_StartupScreenSurvivesGeometryChange is bugs.md B3: the browser's
// first resize must not corrupt the startup screen. A geometry change replaces
// the screen; it must not turn the screen it replaces into transcript history,
// and after it settles the grid must hold exactly what a session started at
// that geometry holds.
func TestWebUI_StartupScreenSurvivesGeometryChange(t *testing.T) {
	vt, sc, sink := newB3Session(t, 100, 30)

	// (1) The startup screen is complete: the mascot's first row is on screen
	// and no row of it was manufactured into history by the startup itself.
	if !b3HeaderComplete(t, vt) {
		t.Errorf("startup screen is scrolled at 100x30 (mascot cut):\n%s", strings.Join(b3Rows(vt)[:12], "\n"))
	}
	if shipped := sink.shipped(); len(shipped) != 0 {
		t.Errorf("startup shipped %d history rows before any resize: %q", len(shipped), shipped)
	}

	// (2) The browser's first resize away from the server geometry: shrink then
	// grow, rendering after each (the client paints what the server publishes).
	vt.Resize(90, 24)
	b3Render(sc)
	vt.Resize(140, 40)
	b3Render(sc)

	// (3) Settled: the grid equals a fresh session's screen at that geometry.
	fresh, _, _ := newB3Session(t, 140, 40)
	got, want := vt.Grid().Text(), fresh.Grid().Text()
	if got != want {
		g, w := b3Rows(vt), b3Rows(fresh)
		n := len(g)
		if len(w) < n {
			n = len(w)
		}
		for i := 0; i < n; i++ {
			if g[i] != w[i] {
				t.Errorf("grid differs from a fresh %dx%d session at row %d\n got: %q\nwant: %q", 140, 40, i, g[i], w[i])
				break
			}
		}
		if len(g) != len(w) {
			t.Errorf("grid rows %d != fresh %d", len(g), len(w))
		}
	}
	if !b3HeaderComplete(t, vt) {
		t.Errorf("header cut after the resize sequence:\n%s", strings.Join(b3Rows(vt)[:12], "\n"))
	}

	// (4) No row of the current screen is present as history, and the geometry
	// change shipped no history at all: the startup screen fits, so nothing
	// scrolled and the browser's transcript is empty.
	if sb := vt.Grid().Scrollback(); len(sb) != 0 {
		t.Errorf("geometry change left %d scrollback rows", len(sb))
	}
	if shipped := sink.shipped(); len(shipped) != 0 {
		t.Errorf("geometry change shipped %d transcript rows: %q", len(shipped), shipped)
	}

	// A trailing resize back to the original geometry stays consistent too
	// (a grow that makes the screen taller than its content must re-anchor).
	vt.Resize(100, 30)
	b3Render(sc)
	back, _, _ := newB3Session(t, 100, 30)
	if got, want := vt.Grid().Text(), back.Grid().Text(); got != want {
		t.Errorf("grid after round-trip resize != fresh 100x30 session")
	}
	if !b3HeaderComplete(t, vt) {
		t.Errorf("header cut after round-trip resize:\n%s", strings.Join(b3Rows(vt)[:12], "\n"))
	}
}

// TestWebUI_TranscriptRowsAreNotOnScreen is the invariant behind B3's
// "no row of the current screen present as history", stated over the frames a
// browser actually receives: every shipped transcript row must be off-screen
// at the moment it ships. Rows repeated verbatim on the screen (box borders)
// are excluded: their presence on screen is not the shipping bug this guards.
func TestWebUI_TranscriptRowsAreNotOnScreen(t *testing.T) {
	vt, sc, sink := newB3Session(t, 100, 30)

	for _, geo := range [][2]int{{90, 24}, {140, 40}, {120, 12}, {100, 30}} {
		vt.Resize(geo[0], geo[1])
		b3Render(sc)
	}

	for i, fr := range sink.frames {
		for _, row := range fr.rows {
			if strings.TrimSpace(row) == "" {
				continue
			}
			count := 0
			for _, g := range fr.screen {
				if g == row {
					count++
				}
			}
			if count == 1 {
				t.Errorf("frame %d shipped %q as history while the screen showed it", i, row)
			}
		}
	}
}
