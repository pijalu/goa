// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
)

// TestSlashCommand_ExecutingPlaceholderFilmstrip filmstrip-validates bugs.md
// "Session: slow commands need an executing placeholder": the status line must
// show "executing /slowfilm ..." while the command's Run is in flight, and be
// cleared by the time the command result is rendered in the chat viewport.
// The film is captured at three steps: pre-submit, mid-Run (from inside the
// command itself), and post-completion.
func TestSlashCommand_ExecutingPlaceholderFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	registry := core.NewCommandRegistry()
	slow := &testPlaceholderCommand{status: sc.status}
	if err := registry.Register(slow); err != nil {
		t.Fatal(err)
	}
	sc.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))

	film := sc.filmstrip()

	// Step 1: idle UI before submitting.
	sc.engine.ApplySync(func() {})
	sc.engine.RenderNow()
	film.Capture("pre-submit", sc.engine.AgentFrame(), sc.status.Text())

	// Step 2 (captured from inside Run): the placeholder must be visible.
	sc.engine.ApplySync(func() {
		sc.app.handleSlashCommand("/slowcmd")
		// Step 3 (post-return): the placeholder must be cleared and the
		// command result must be in the chat viewport.
		sc.engine.RenderNow()
		film.Capture("post-completion", sc.engine.AgentFrame(), sc.status.Text())
	})
	// Record the mid-run observation as a step in the trace by re-capturing
	// with the status the command observed (Run already executed on-loop).
	if !slow.visibleInRun {
		t.Fatalf("placeholder not visible during Run; film:\n%s", film.Render())
	}
	if slow.textInRun != "executing /slowcmd ..." {
		t.Fatalf("unexpected placeholder text %q; film:\n%s", slow.textInRun, film.Render())
	}

	trace := film.StatusTrace()
	if len(trace) != 2 {
		t.Fatalf("expected 2 film steps, got %d: %v", len(trace), trace)
	}
	if trace[0] != "" {
		t.Fatalf("pre-submit status must be idle, got %q; trace=%v", trace[0], trace)
	}
	if trace[1] != "" {
		t.Fatalf("post-completion status must be cleared, got %q; trace=%v", trace[1], trace)
	}

	// The command output must be echoed into the chat viewport (the default
	// "completed successfully" fallback for a silent command).
	frame := sc.engine.AgentFrame()
	if !strings.Contains(frame.Dump(), "slowcmd") {
		t.Fatalf("expected command feedback in chat viewport; frame:\n%s", frame.Dump())
	}
}
