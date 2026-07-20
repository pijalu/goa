// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"strings"
	"testing"
)

// TestEphemeralSystemMessage_StrippedAtTurnEnd verifies that ephemeral system
// nudges (recovery/repeat hints) are removed from history at turn end so they
// do not pollute the next turn's context, while durable system messages persist.
func TestEphemeralSystemMessage_StrippedAtTurnEnd(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})

	a.InjectSystemMessage("durable tool-change notice")
	a.InjectEphemeralSystemMessage("transient recovery hint")

	a.mu.Lock()
	before := len(a.history)
	a.mu.Unlock()
	if before != 2 {
		t.Fatalf("expected 2 system messages before strip, got %d", before)
	}

	// The ephemeral message must still be present (sent to the model) BEFORE
	// the turn-end strip.
	a.mu.Lock()
	hasEphemeral := false
	for _, m := range a.history {
		if m.Role == System && strings.Contains(m.Content, "recovery hint") {
			hasEphemeral = true
		}
	}
	a.mu.Unlock()
	if !hasEphemeral {
		t.Fatal("ephemeral message should be present before the turn-end strip")
	}

	a.stripEphemeralSystemMessages()

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.history) != 1 {
		t.Fatalf("expected 1 message after strip (durable kept), got %d: %+v", len(a.history), a.history)
	}
	if a.history[0].Content != "durable tool-change notice" {
		t.Errorf("durable system message should survive, got %q", a.history[0].Content)
	}
	for _, m := range a.history {
		if strings.Contains(m.Content, "recovery hint") {
			t.Errorf("ephemeral recovery hint was not stripped: %q", m.Content)
		}
	}
}

// TestEphemeralSystemMessage_TagNotSentToModel verifies the ephemeral tag is
// local only: migrateMessage does not forward Message.Metadata, so the provider
// message carries the content but not the ephemeral marker.
func TestEphemeralSystemMessage_TagNotSentToModel(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	a.InjectEphemeralSystemMessage("transient nudge")

	a.mu.Lock()
	msg := a.history[len(a.history)-1]
	a.mu.Unlock()

	pm := migrateMessage(msg)
	if pm.Role != "system" {
		t.Errorf("expected system role, got %q", pm.Role)
	}
	// The content is delivered (the model needs the nudge during the turn)...
	if len(pm.Content) == 0 || pm.Content[0].Text != "transient nudge" {
		t.Errorf("ephemeral content should be sent to the model, got %+v", pm.Content)
	}
	// ...but provider.Message has no ephemeral field, so the tag cannot leak.
}

// TestEphemeralSystemMessage_NotEmittedToObservers is the regression for the
// "hidden steering" bug: the [goa-system] round-limit nudge was emitted as a
// content event, so the TUI rendered an internal control message and the
// model parroted it as a user-facing "budget". Ephemeral injections must
// reach history (the model sees them) without producing observer events.
func TestEphemeralSystemMessage_NotEmittedToObservers(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.InjectEphemeralSystemMessage("[goa-system] internal control note")

	// History receives the message (model-visible during the turn)...
	a.mu.Lock()
	histLen := len(a.history)
	a.mu.Unlock()
	if histLen != 1 {
		t.Fatalf("expected 1 history entry after injection, got %d", histLen)
	}
	// ...but no event is emitted to observers.
	if got := len(obs.Events()); got != 0 {
		t.Fatalf("ephemeral injection leaked %d observer events: %+v", got, obs.Events())
	}
}
