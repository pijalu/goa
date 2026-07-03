// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"time"

	"github.com/pijalu/goa/tui"
)

// startPerfLoad runs a synthetic streaming session inside the real TUI so the
// terminal path can be profiled under load without calling an LLM.
func (a *App) startPerfLoad() {
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
		ticker := time.NewTicker(16 * time.Millisecond)
		defer ticker.Stop()

		for time.Since(start) < duration {
			select {
			case <-ticker.C:
			case <-a.subs.tuiEngine.Stopped():
				return
			}
			line := fmt.Sprintf("Performance load line %d: the quick brown fox jumps over the lazy dog while the terminal wraps and renders ansi.", i)
			a.apply(func() {
				a.subs.chat.UpdateLastMessage(line, tui.ConsoleAssistantMessage)
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
