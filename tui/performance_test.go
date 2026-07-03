// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// renderCounter is a Component that records how many times Render is called.
type renderCounter struct {
	lines []string
	count int
}

func (r *renderCounter) Render(width int) []string {
	r.count++
	return r.lines
}

func (r *renderCounter) HandleInput(string) {}
func (r *renderCounter) Invalidate()       {}

// TestChatViewport_PerEntryCache proves that only the changed entry is
// re-rendered during streaming-style updates, not the entire conversation.
func TestChatViewport_PerEntryCache(t *testing.T) {
	cv := NewChatViewport()
	var counters []*renderCounter
	for i := 0; i < 50; i++ {
		c := &renderCounter{lines: []string{fmt.Sprintf("line %d", i)}}
		counters = append(counters, c)
		cv.AddComponent(c)
	}
	cv.Render(80)
	for i, c := range counters {
		if c.count != 1 {
			t.Fatalf("entry %d rendered %d times on first frame, want 1", i, c.count)
		}
	}

	cv.UpdateLast(nil, func(e *MessageEntry) {
		rc := e.View.(*renderCounter)
		rc.lines = []string{"updated"}
	})
	cv.Render(80)
	for i, c := range counters {
		want := 1
		if i == len(counters)-1 {
			want = 2
		}
		if c.count != want {
			t.Fatalf("entry %d rendered %d times after update, want %d", i, c.count, want)
		}
	}
}

// BenchmarkChatViewport_StreamAppend measures the cost of repeatedly updating
// the last assistant message, which is the common streaming path.
func BenchmarkChatViewport_StreamAppend(b *testing.B) {
	cv := NewChatViewport()
	cv.AddAssistantMessage("")
	text := strings.Builder{}
	for i := 0; i < b.N; i++ {
		text.WriteString("word ")
		cv.UpdateLastMessage(text.String(), ConsoleAssistantMessage)
		cv.Render(80)
	}
}

var cupPattern = regexp.MustCompile(`\x1b\[[0-9;]*H`)

func countCUP(s string) int { return len(cupPattern.FindAllString(s, -1)) }

// TestCompositor_OnlyRedrawsVisibleChanges proves that the compositor diffs
// only the visible region: a change to the last line of a 100-line canvas on a
// 24-row terminal should produce just one CUP update, not 100.
func TestCompositor_OnlyRedrawsVisibleChanges(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	comp := NewCompositor(term)

	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "unchanged line"
	}
	scene := &Scene{
		TerminalW: 80,
		TerminalH: 24,
		Layers: []Layer{{
			Name:    "chat",
			Kind:    LayerBase,
			Rect:    Rect{W: 80, H: len(lines)},
			Content: lines,
		}},
	}
	comp.Render(scene)
	term.writes = nil

	lines[len(lines)-1] = "changed line"
	comp.Render(scene)

	out := strings.Join(term.Writes(), "")
	cupCount := countCUP(out)
	// One CUP for the changed line plus one for the hardware cursor is expected;
	// small-scroll output may also include the CUP used to position the scroll.
	if cupCount > 4 {
		t.Fatalf("expected at most 4 CUP sequences for a single-line change, got %d; output:\n%s", cupCount, out)
	}
}
