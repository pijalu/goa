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

// TestEphemeralSystemMessage_NotEmittedAsContentEvent is the regression for
// the "hidden steering" bug: the [goa-system] round-limit nudge was emitted
// as a content event, so the TUI rendered an internal control message and
// the model parroted it as a user-facing "budget". Ephemeral injections must
// reach history (the model sees them) without producing CONTENT observer
// events. They DO emit an EventProgress for user visibility (guardrail
// notification), but never an EventContent.
func TestEphemeralSystemMessage_NotEmittedAsContentEvent(t *testing.T) {
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
	// ...but no CONTENT event is emitted to observers.
	for _, e := range obs.Events() {
		if e.Type == EventContent {
			t.Fatalf("ephemeral injection leaked EventContent: %+v", e)
		}
	}
}

// TestConsecutiveToolRounds_NudgeFiresOncePerTurn verifies the forced-answer
// nudge fires at most once per turn, so legitimate long investigations are not
// interrupted by a repeating nudge/answer cycle (bugs.md hidden-steering).
func TestConsecutiveToolRounds_NudgeFiresOncePerTurn(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	countEphemeral := func() int {
		a.mu.Lock()
		defer a.mu.Unlock()
		n := 0
		for _, m := range a.history {
			if m.Metadata[metaEphemeral] == "true" {
				n++
			}
		}
		return n
	}

	// First streak reaching the limit → one nudge.
	a.checkConsecutiveToolRounds() // round 1
	a.checkConsecutiveToolRounds() // round 2 → nudge
	if got := countEphemeral(); got != 1 {
		t.Fatalf("after first streak, ephemeral nudges = %d, want 1", got)
	}

	// Second streak reaching the limit in the same turn → no additional nudge.
	a.checkConsecutiveToolRounds() // round 1
	a.checkConsecutiveToolRounds() // round 2 → suppressed (already fired)
	if got := countEphemeral(); got != 1 {
		t.Fatalf("after second streak, ephemeral nudges = %d, want still 1 (once per turn)", got)
	}
}

// TestEffectiveMaxConsecutiveToolRounds_DefaultRaised verifies the default
// consecutive-tool-round limit is 15 (raised from 10 to avoid interrupting
// legitimate long investigations).
func TestEffectiveMaxConsecutiveToolRounds_DefaultRaised(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	if got := a.effectiveMaxConsecutiveToolRounds(); got != 15 {
		t.Errorf("default = %d, want 15", got)
	}
}

// TestTrackToolCallingRound_ThinkingResetsStreak verifies that a round with
// thinking tokens (reasoning) resets the consecutive tool-round counter —
// the model was actively reasoning, not idling. This is the fix for the bug
// where thinking-heavy models were nudged mid-task because thinking tokens
// flowed to thinkingBuf, not contentBuf.
func TestTrackToolCallingRound_ThinkingResetsStreak(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 3})

	// Simulate 5 consecutive rounds: each has thinking tokens + tool calls
	// but NO visible content. Without the fix, each round increments the
	// streak; after 3 rounds the nudge fires. With the fix, every round
	// resets the streak because thinkingBuf is non-empty.
	for i := 0; i < 5; i++ {
		// Simulate a round: thinking tokens were streamed (reasoning).
		a.thinkingBuf.WriteString("reasoning step")
		a.trackToolCallingRound()
		a.mu.Lock()
		streak := a.consecutiveToolRounds
		a.mu.Unlock()
		if streak != 0 {
			t.Fatalf("round %d: streak = %d, want 0 (thinking should reset)", i+1, streak)
		}
		a.thinkingBuf.Reset()
	}

	// Now simulate rounds with NO thinking and NO content — these should
	// increment the streak. At round 3 the nudge fires and the counter
	// resets to 0, so we check rounds 1-2 only.
	for i := 1; i <= 2; i++ {
		a.trackToolCallingRound()
		a.mu.Lock()
		streak := a.consecutiveToolRounds
		a.mu.Unlock()
		if streak != i {
			t.Fatalf("silent round %d: streak = %d, want %d", i, streak, i)
		}
	}
}

// TestTrackToolCallingRound_NudgeNotFiredWithThinking verifies that the
// forced-answer nudge does NOT fire when the model is actively reasoning
// (thinking tokens present), even after many consecutive tool rounds.
func TestTrackToolCallingRound_NudgeNotFiredWithThinking(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	// Simulate 5 rounds, each with thinking + tool calls (no visible text).
	// The nudge should never fire because thinking resets the streak.
	for i := 0; i < 5; i++ {
		a.thinkingBuf.WriteString("thinking")
		a.trackToolCallingRound()
		a.thinkingBuf.Reset()
	}

	// Verify no ephemeral nudge was injected.
	a.mu.Lock()
	n := 0
	for _, m := range a.history {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("ephemeral nudges = %d, want 0 (thinking rounds should not trigger nudge)", n)
	}
}

// TestTrackToolCallingRound_NudgeFiredWithoutThinking verifies that the
// forced-answer nudge DOES fire when rounds have neither content nor thinking.
func TestTrackToolCallingRound_NudgeFiredWithoutThinking(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	// Two silent rounds (no content, no thinking, tool calls only).
	a.trackToolCallingRound()
	a.trackToolCallingRound()

	// The nudge should have fired.
	a.mu.Lock()
	n := 0
	for _, m := range a.history {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	a.mu.Unlock()
	if n != 1 {
		t.Fatalf("ephemeral nudges = %d, want 1 (silent rounds should trigger nudge)", n)
	}
}

// TestInjectEphemeralSystemMessage_EmitsProgressEvent verifies that injecting
// an ephemeral system message also emits an EventProgress so the user sees
// the guardrail notification in the TUI (bugs.md host-control-note-invisible).
func TestInjectEphemeralSystemMessage_EmitsProgressEvent(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.InjectEphemeralSystemMessage("[goa-system] Internal control note: forced answer nudge")

	events := obs.Events()
	found := false
	for _, e := range events {
		if e.Type == EventProgress && strings.Contains(e.Text, "guardrail") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected EventProgress with 'guardrail' text, got %d events: %+v", len(events), events)
	}
}

// TestInjectEphemeralSystemMessage_NoProgressEventForNonControl verifies that
// non-control ephemeral messages (not starting with "[goa-system]") do NOT
// emit a progress event — only host control notes need user visibility.
func TestInjectEphemeralSystemMessage_NoProgressEventForNonControl(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.InjectEphemeralSystemMessage("regular ephemeral system message")

	events := obs.Events()
	for _, e := range events {
		if e.Type == EventProgress {
			t.Fatalf("unexpected EventProgress for non-control message: %+v", e)
		}
	}
}

// TestMaxConsecutiveToolRounds_ZeroDisables verifies that setting the limit
// to 0 disables the forced-answer nudge entirely.
func TestMaxConsecutiveToolRounds_ZeroDisables(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: -1})

	// Run many silent rounds — nudge should never fire.
	for i := 0; i < 20; i++ {
		a.trackToolCallingRound()
	}

	a.mu.Lock()
	n := 0
	for _, m := range a.history {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("ephemeral nudges = %d, want 0 (limit disabled)", n)
	}
}

// TestMaxConsecutiveToolRounds_CustomThreshold verifies the nudge fires at
// the configured threshold, not the default of 15.
func TestMaxConsecutiveToolRounds_CustomThreshold(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 3})

	// 2 rounds — below threshold.
	a.trackToolCallingRound()
	a.trackToolCallingRound()

	a.mu.Lock()
	n := 0
	for _, m := range a.history {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("after 2 rounds: nudges = %d, want 0 (below threshold)", n)
	}

	// 3rd round — threshold reached.
	a.trackToolCallingRound()

	a.mu.Lock()
	n = 0
	for _, m := range a.history {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	a.mu.Unlock()
	if n != 1 {
		t.Fatalf("after 3 rounds: nudges = %d, want 1 (threshold reached)", n)
	}
}
