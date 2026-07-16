// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/event"
)

// A foreground `agent` tool sub-agent streams its work into the orchestrator
// event stream labeled by its description. The app must render those labeled
// InterAgent messages as agent-attributed blocks in the chat — so the user
// sees the sub-agent working live, attributed to its task, not a blank wait.
// End-to-end filmstrip validation of the C1 transparency fix.
func TestInterAgentEvent_SubAgentStream_RendersLabeledAgentBlock(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Simulate the labeled sub-agent stream the C1 fix emits via the
	// orchestrator: thinking, content, and a tool call, all From=<description>.
	events := []*event.InterAgent{
		{From: "fix login bug", To: "user", Content: "[thinking] checking the auth flow"},
		{From: "fix login bug", To: "user", Content: "[tool] read {\"path\":\"auth.go\"}"},
		{From: "fix login bug", To: "user", Content: "The bug is a nil deref in validate()"},
	}
	for _, ie := range events {
		ev := ie
		sc.engine.ApplySync(func() {
			sc.app.handleInterAgentEvent(ev)
		})
		sc.engine.RenderNow()
	}

	// Read the visible UI as data from the agent frame (harness pattern).
	frame := sc.engine.AgentFrame()
	visible := joinLines(frame.Visible)
	for _, want := range []string{"fix login bug", "checking the auth flow", "nil deref"} {
		if !contains(visible, want) {
			t.Errorf("chat missing sub-agent content %q.\nFrame:\n%s", want, frame.Dump())
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
