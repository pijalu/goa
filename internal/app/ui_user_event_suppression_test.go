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

// TestUIScenario_UserContentEventNotDuplicated verifies that a live user
// content event does NOT add a second user message to the chat history.
func TestUIScenario_UserContentEventNotDuplicated(t *testing.T) {
	sc := newUIScenario(t, 150, 29)

	msg := "can you try to run a simple hello world python to test the tooling ?"

	sc.engine.ApplySync(func() {
		sc.chat.AddUserMessage(msg)
	})
	sc.engine.RenderNow()
	sc.film.Capture("after_submit", sc.engine.AgentFrame(), sc.status.Text())

	// Apply the live user content event that the agent would emit.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.User, Text: msg})

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

	// Check chat history snapshot
	snap := sc.chat.Snapshot()
	userCount := 0
	for _, m := range snap {
		if m.Type == 0 && m.Text == msg { // ConsoleUserMessage
			userCount++
		}
	}
	if userCount != 1 {
		t.Errorf("chat history has %d user messages, want 1", userCount)
	}
}
