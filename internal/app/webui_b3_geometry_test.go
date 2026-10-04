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

// b3Sink records what a browser receives: the transcript rows each frame
// shipped (the rows that scrolled off the top) and the screen those rows were
// shipped against.
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
	// The rest of the production startup banner (sticky skills, context cost,
	// provider line): five banner entries in total, the shape that overflowed a
	// small screen and scrolled the mascot into history (bugs.md B3).
	addStartupInfo(sc.chat, "⟡ Sticky skills (always-on): telegram, thoughtfull")
	addStartupInfo(sc.chat, promptContextBanner("system", nil))
	addStartupInfo(sc.chat, "⟡ Connected to OpenCode Zen Go (deepseek-v4-flash).")
	b3Render(sc)
	return vt, sc, sink
}

func b3Render(sc *uiScenario) {
	sc.engine.ApplySync(func() {})
	sc.engine.RenderNow()
}

func b3Rows(vt *webui.VirtualTerminal) []string { return strings.Split(vt.Grid().Text(), "\n") }

// b3HeaderComplete reports whether the grid shows the mascot from its first
// row — i.e. the startup screen has not been scrolled. A scrolled screen starts
// mid-art, so its first row is one of the narrower art rows.
func b3HeaderComplete(vt *webui.VirtualTerminal) bool {
	rows := b3Rows(vt)
	return len(rows) > 0 && strings.HasPrefix(strings.TrimRight(rows[0], " "), "          ▄▄▄▄▄")
}

// b3RequireWholeStartup fails unless the grid holds the whole startup screen,
// nothing was shipped as history, and no scrollback row equals a grid row.
func b3RequireWholeStartup(t *testing.T, label string, vt *webui.VirtualTerminal, sink *b3Sink) {
	t.Helper()
	if !b3HeaderComplete(vt) {
		t.Errorf("%s: startup screen scrolled (mascot cut):\n%s", label, strings.Join(b3Rows(vt)[:8], "\n"))
	}
	if shipped := sink.shipped(); len(shipped) != 0 {
		t.Errorf("%s: %d transcript rows shipped: %q", label, len(shipped), shipped)
	}
	// Literal B3 invariant: no scrollback row equals a grid row.
	grid := vt.Grid()
	screen := b3Rows(vt)
	for _, sbRow := range grid.Scrollback() {
		var b strings.Builder
		for _, c := range sbRow {
			if c.Text == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Text)
			}
		}
		row := strings.TrimRight(b.String(), " ")
		for i, g := range screen {
			if strings.TrimRight(g, " ") == row {
				t.Errorf("%s: scrollback row %q equals grid row %d", label, row, i)
			}
		}
	}
}

// b3RequireSameScreen fails unless vt's screen is byte-identical to a fresh
// session rendered at the same geometry.
func b3RequireSameScreen(t *testing.T, label string, vt *webui.VirtualTerminal, cols, rows int) {
	t.Helper()
	fresh, _, _ := newB3Session(t, cols, rows)
	got, want := b3Rows(vt), b3Rows(fresh)
	if len(got) != len(want) {
		t.Errorf("%s: %d rows != fresh %dx%d session's %d", label, len(got), cols, rows, len(want))
	}
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			t.Errorf("%s: row %d differs from a fresh %dx%d session\n got: %q\nwant: %q", label, i, cols, rows, got[i], want[i])
			return
		}
	}
}

// TestWebUI_StartupScreenSurvivesGeometryChange is bugs.md B3: the browser's
// first resize must not corrupt the startup screen. A geometry change replaces
// the screen; the rows it displaces belong to the screen being replaced, never
// to the browser's transcript, and after it settles the grid must hold exactly
// what a session started at that geometry holds.
func TestWebUI_StartupScreenSurvivesGeometryChange(t *testing.T) {
	vt, sc, sink := newB3Session(t, 100, 30)
	b3RequireWholeStartup(t, "startup at 100x30", vt, sink)

	// The browser's first resize away from the server geometry: shrink then
	// grow, rendering after each, as the client paints what the server sends.
	vt.Resize(90, 24)
	b3Render(sc)
	vt.Resize(140, 40)
	b3Render(sc)

	b3RequireSameScreen(t, "settled after shrink+grow", vt, 140, 40)
	b3RequireWholeStartup(t, "settled after shrink+grow", vt, sink)
	if sb := vt.Grid().Scrollback(); len(sb) != 0 {
		t.Errorf("geometry change left %d scrollback rows", len(sb))
	}

	// A resize back to the original geometry stays consistent too.
	vt.Resize(100, 30)
	b3Render(sc)
	b3RequireSameScreen(t, "after round-trip resize", vt, 100, 30)
	b3RequireWholeStartup(t, "after round-trip resize", vt, sink)
}

// TestWebUI_TranscriptRowsAreNotOnScreen states B3's invariant over the frames a
// browser actually receives: every shipped transcript row must be off-screen at
// the moment it ships. Rows repeated verbatim on the screen (box borders) are
// excluded — their presence on screen is not the shipping bug this guards.
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
			if b3OnScreenOnce(fr.screen, row) {
				t.Errorf("frame %d shipped %q as history while the screen showed it", i, row)
			}
		}
	}
}

// b3OnScreenOnce reports whether row appears exactly once in screen: a single
// occurrence is the current screen's own content, while a repeated row is an
// ambiguous decoration (a box border) that says nothing about shipping.
func b3OnScreenOnce(screen []string, row string) bool {
	count := 0
	for _, g := range screen {
		if g == row {
			count++
		}
	}
	return count == 1
}
