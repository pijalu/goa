// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestNewSessionClearsTranscript reproduces bugs.md Issue 3: after /new the screen
// must be cleanly redrawn — none of the pre-/new conversation may linger in the
// chat viewport or the rendered frame.
func TestNewSessionClearsTranscript(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Populate a session with enough content to require scrolling.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	for i := 0; i < 30; i++ {
		sc.apply(&agentic.OutputEvent{
			Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant,
			Text: fmt.Sprintf("pre-new assistant line %d with some padding text to wrap. ", i), IsDelta: true,
		})
	}
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Sanity: content is present before /new.
	before := sc.chat.Messages()
	if len(before) == 0 {
		t.Fatalf("precondition: expected conversation content before /new")
	}

	// Drive /new through the real control path.
	sc.engine.ApplySync(func() {
		sc.app.handleNewSession()
	})
	sc.engine.RenderNow()
	frame := sc.engine.AgentFrame()
	sc.film.Capture("after_new", frame, sc.status.Text())

	// The viewport must not retain any pre-/new assistant text.
	for _, m := range sc.chat.Messages() {
		if strings.Contains(m.Content, "pre-new assistant line") {
			t.Fatalf("stale pre-/new content retained in viewport: %q", m.Content)
		}
	}
	// The rendered frame must not show stale content either.
	dump := frame.Dump()
	if strings.Contains(dump, "pre-new assistant line") {
		t.Fatalf("stale pre-/new content rendered after /new:\n%s", dump)
	}
}
