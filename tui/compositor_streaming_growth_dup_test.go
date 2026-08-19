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

	assertNoConsecutiveAddedLineDuplicates(t, strip.Frames())
	assertNoReplayDuplicates(t, term, content)
}

func assertNoConsecutiveAddedLineDuplicates(t *testing.T, frames []Snapshot) {
	t.Helper()
	for i := 1; i < len(frames); i++ {
		previous := streamLineSet(frames[i-1].Diff.AddedLines)
		for _, line := range frames[i].Diff.AddedLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && previous[trimmed] {
				t.Errorf("frame %d (%s): line %q duplicated across consecutive AddedLines", i, frames[i].Label, trimmed)
			}
		}
	}
}

func streamLineSet(lines []string) map[string]bool {
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		seen[strings.TrimSpace(line)] = true
	}
	return seen
}

func assertNoReplayDuplicates(t *testing.T, term *fakeTerminal, content []string) {
	t.Helper()
	emu := newScreenEmulator(term.h, term.w)
	for _, write := range term.writes {
		emu.Process(write)
	}
	all := append([]string{}, emu.Scrollback()...)
	for row := 0; row < term.h; row++ {
		all = append(all, emu.Visible(row))
	}
	for index := range content {
		want := fmt.Sprintf("BASE-%02d", index)
		if index >= 12 {
			want = fmt.Sprintf("GROW-%03d", index)
		}
		if countExactLine(all, want) > 1 {
			t.Errorf("%s appears more than once in scrollback+screen", want)
		}
	}
}

func countExactLine(lines []string, want string) int {
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}
