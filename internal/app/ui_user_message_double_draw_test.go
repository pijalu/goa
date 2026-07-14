// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TestUIScenario_UserMessageNotDuplicated regresses the reported double-draw
// where the same user message appeared twice in the chat viewport after a
// model-switch flash and the start of agent thinking.
func TestUIScenario_UserMessageNotDuplicated(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	msg := "can you try to run a simple hello world python to test the tooling ?"

	sc.engine.ApplySync(func() {
		sc.chat.AddFlashMessage("⚡ Switched to model: google/gemma-4-e4b")
		sc.chat.AddUserMessage(msg)
	})
	sc.engine.RenderNow()
	sc.film.Capture("after_submit", sc.engine.AgentFrame(), sc.status.Text())

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "I should use the python tool."})

	film := sc.filmstrip()
	t.Logf("filmstrip:\n%s", film.Render())

	for i, s := range film.Frames() {
		visible := strings.Join(s.Frame.Visible, "\n")
		stripped := ansi.Strip(visible)
		count := strings.Count(stripped, msg)
		if count > 1 {
			t.Errorf("step %d (%s): user message rendered %d times; visible:\n%s", i, s.Label, count, stripped)
		}
	}

	last := film.Last()
	if last == nil {
		t.Fatal("expected at least one filmstrip frame")
	}
	visible := strings.Join(last.Frame.Visible, "\n")
	stripped := ansi.Strip(visible)
	count := strings.Count(stripped, msg)
	if count != 1 {
		t.Errorf("user message rendered %d times in final frame, want exactly 1; last visible:\n%s", count, stripped)
	}
}
