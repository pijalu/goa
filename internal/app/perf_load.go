// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/tui"
)

// perfLoadTick is the streaming cadence of the synthetic load (one update per
// tick, like a model emitting tokens).
const perfLoadTick = 16 * time.Millisecond

// perfLoadMaxLines caps the growing answer so a long run stays a bounded memory
// test (the transcript is real, unbounded growth is not the point).
const perfLoadMaxLines = 4000

// perfLoadGrower accumulates the synthetic streamed answer line by line. It
// keeps the line count so the cap check stays O(1) — the generator must not add
// per-tick work that would distort the very measurement it exists for.
type perfLoadGrower struct {
	b     strings.Builder
	lines int
}

// append returns the message body after appending line.
func (g *perfLoadGrower) append(line string) string {
	if g.lines >= perfLoadMaxLines {
		g.b.Reset()
		g.lines = 0
	}
	if g.lines > 0 {
		g.b.WriteByte('\n')
	}
	g.b.WriteString(line)
	g.lines++
	return g.b.String()
}

// startPerfLoad runs a synthetic streaming session inside the real TUI so the
// terminal path can be profiled under load without calling an LLM.
//
// Two shapes:
//   - grow (default): every tick APPENDS one line to a single assistant message
//     — a real streaming turn, and the shape the "CPU climbs as the answer
//     grows" report describes. Per-chunk cost must stay flat as the answer
//     grows past the screen; this mode exposes per-chunk work that is
//     O(answer).
//   - replace (--perf-load-grow=false): every tick REPLACES the message text
//     with one line. Steady-state single-line cost, no growth.
func (a *App) startPerfLoad() {
	grow := a.subs.perfLoadGrow
	go func() {
		duration := a.subs.perfLoadDuration
		if duration <= 0 {
			duration = 30 * time.Second
		}

		// Give the TUI time to finish its first render before we start
		// hammering the command loop.
		time.Sleep(200 * time.Millisecond)

		a.apply(func() {
			a.subs.chat.AddAssistantMessage("Performance load started...")
		})

		start := time.Now()
		i := 0
		var grower perfLoadGrower
		ticker := time.NewTicker(perfLoadTick)
		defer ticker.Stop()

		for time.Since(start) < duration {
			select {
			case <-ticker.C:
			case <-a.subs.tuiEngine.Stopped():
				return
			}
			line := fmt.Sprintf("Performance load line %d: the quick brown fox jumps over the lazy dog while the terminal wraps and renders ansi.", i)
			a.apply(func() {
				content := line
				if grow {
					content = grower.append(line)
				}
				a.subs.chat.UpdateLastMessage(content, tui.ConsoleAssistantMessage)
			})
			i++
		}

		a.apply(func() {
			a.subs.chat.AddSystemMessage("Performance load complete.")
		})
		time.Sleep(100 * time.Millisecond)
		a.subs.tuiEngine.Stop()
	}()
}
