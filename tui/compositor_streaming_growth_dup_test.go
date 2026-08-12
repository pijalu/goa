// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_StreamingContentGrowthNoDuplication is the regression test for
// must-fix #6: "TUI tool-call rendering: duplicated history line on
// content-size change." When a tool call's rendered height grows across frames
// (streaming arg growth, collapse→expand), a height delta at the history↔screen
// boundary can double-emit the boundary line — once pushed into scrollback and
// once re-rendered in the viewport. This streams a growing base layer (the
// realistic tool-widget-grows shape) across many frames and asserts, via both
// the filmstrip (no duplicated AddedLines between consecutive frames) and the
// replayed terminal (no content line appears more than once), that the boundary
// is never duplicated.
func TestCompositor_StreamingContentGrowthNoDuplication(t *testing.T) {
	const (
		termH = 10
		w     = 40
	)
	term := &fakeTerminal{w: w, h: termH}
	comp := NewCompositor(term)
	scene := func(lines []string) *Scene {
		return &Scene{
			TerminalW: w, TerminalH: termH,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(lines)}, Content: lines}},
		}
	}

	// Seed a baseline transcript that already overflows the viewport so the
	// history↔screen boundary is established.
	var content []string
	for i := 0; i < 12; i++ {
		content = append(content, fmt.Sprintf("BASE-%02d", i))
	}
	comp.Render(scene(content))

	// Capture filmstrip frames as the content grows by a few lines per frame —
	// the exact shape of a streaming tool widget whose rendered height changes.
	strip := NewFilmstrip()
	strip.Capture("seed", scene(content).AgentFrame(termH), "")

	for step := 0; step < 40; step++ {
		// Grow by a variable amount (1..3 lines) so the per-frame height delta
		// differs — the condition the bug report says triggers duplication.
		grow := (step % 3) + 1
		for i := 0; i < grow; i++ {
			content = append(content, fmt.Sprintf("GROW-%03d", len(content)))
		}
		comp.Render(scene(content))
		strip.Capture(fmt.Sprintf("grow-%d", step), scene(content).AgentFrame(termH), "")
	}

	// Collapse→expand cycle: shrink the rendered content (simulate a tool widget
	// collapsing) for one frame, then grow it back — the exact "content-size
	// change mid-render" trigger from the bug report. The boundary must still
	// not duplicate.
	collapsed := append([]string{}, content[:len(content)-6]...)
	comp.Render(scene(collapsed))
	strip.Capture("collapse", scene(collapsed).AgentFrame(termH), "")
	comp.Render(scene(content))
	strip.Capture("expand", scene(content).AgentFrame(termH), "")

	// Invariant 1 (filmstrip): a line added in one frame must not ALSO appear in
	// the AddedLines of the very next frame — that would mean it was emitted
	// twice at the shifting boundary.
	frames := strip.Frames()
	for i := 1; i < len(frames); i++ {
		prev := frames[i-1].Diff.AddedLines
		cur := frames[i].Diff.AddedLines
		seen := make(map[string]bool, len(prev))
		for _, l := range prev {
			seen[strings.TrimSpace(l)] = true
		}
		for _, l := range cur {
			tl := strings.TrimSpace(l)
			if tl == "" {
				continue
			}
			if seen[tl] {
				t.Errorf("frame %d (%s): line %q duplicated across consecutive AddedLines "+
					"(emitted twice at the history↔screen boundary)", i, frames[i].Label, tl)
			}
		}
	}

	// Invariant 2 (terminal replay): no content line may appear more than once
	// across scrollback + visible screen.
	emu := newScreenEmulator(termH, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	all := append([]string{}, emu.Scrollback()...)
	for r := 0; r < termH; r++ {
		all = append(all, emu.Visible(r))
	}
	countExact := func(want string) int {
		n := 0
		for _, l := range all {
			if strings.TrimSpace(l) == want {
				n++
			}
		}
		return n
	}
	for i := 0; i < 12; i++ {
		if c := countExact(fmt.Sprintf("BASE-%02d", i)); c > 1 {
			t.Errorf("BASE-%02d appears %d times in scrollback+screen (duplicated on content growth)", i, c)
		}
	}
	for idx := 12; idx < len(content); idx++ {
		if c := countExact(fmt.Sprintf("GROW-%03d", idx)); c > 1 {
			t.Errorf("GROW-%03d appears %d times in scrollback+screen (duplicated on content growth)", idx, c)
		}
	}
}
