// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
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

// TestEphemeralSystemMessage_InModelHistoryAndUserBubble verifies that an
// ephemeral host control nudge reaches model history and remains visible to the user.
func TestEphemeralSystemMessage_InModelHistoryAndUserBubble(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	nudge := "[goa-system] internal control note"
	a.InjectEphemeralSystemMessage(nudge)

	// The model sees it in history (during the turn)...
	a.mu.Lock()
	histLen := len(a.history)
	a.mu.Unlock()
	if histLen != 1 {
		t.Fatalf("expected 1 history entry after injection, got %d", histLen)
	}
	found := false
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Metadata["category"] == "system-notification" && e.Text == nudge {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("nudge not surfaced as a system-notification bubble: %+v", obs.Events())
	}
}

// TestConsecutiveToolRounds_LimitReported verifies checkConsecutiveToolRounds
// reports true exactly when the silent-round streak reaches the configured limit
// (Issue 13: convergence is now driven by this signal, not a self-resetting
// nudge, so the caller can run a recovery stream in the same round).
func TestConsecutiveToolRounds_LimitReported(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	if a.checkConsecutiveToolRounds() { // round 1
		t.Fatal("round 1: limit reported, want false (below limit 2)")
	}
	if !a.checkConsecutiveToolRounds() { // round 2 → limit reached
		t.Fatal("round 2: limit not reported, want true (streak reached limit 2)")
	}
	// Streak is NOT self-reset; it stays at the limit so the caller converges now.
	a.mu.Lock()
	streak := a.consecutiveToolRounds
	a.mu.Unlock()
	if streak != 2 {
		t.Fatalf("streak = %d, want 2 (not self-reset; caller owns convergence)", streak)
	}
}

// TestEffectiveMaxConsecutiveToolRounds_DefaultDisabled verifies a bare Agent
// does not invent a hidden limit; application config supplies the normal default.
func TestEffectiveMaxConsecutiveToolRounds_DefaultDisabled(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	if got := a.effectiveMaxConsecutiveToolRounds(); got != 0 {
		t.Errorf("bare-agent limit = %d, want disabled (0)", got)
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

// TestTrackToolCallingRound_LimitNotReachedWithThinking verifies that the
// silent-limit signal is NOT reported when the model is actively reasoning
// (thinking tokens present), even after many consecutive tool rounds.
func TestTrackToolCallingRound_LimitNotReachedWithThinking(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	// Simulate 5 rounds, each with thinking + tool calls (no visible text).
	// The limit must never be reported because thinking resets the streak.
	for i := 0; i < 5; i++ {
		a.thinkingBuf.WriteString("thinking")
		if a.trackToolCallingRound() {
			t.Fatalf("round %d: silent-limit reported, want false (thinking present)", i+1)
		}
		a.thinkingBuf.Reset()
	}
}

// TestTrackToolCallingRound_LimitReachedWithoutThinking verifies that the
// silent-limit signal IS reported when rounds have neither content nor thinking.
func TestTrackToolCallingRound_LimitReachedWithoutThinking(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	// Round 1: below limit. Round 2: limit reached.
	if a.trackToolCallingRound() {
		t.Fatal("round 1: silent-limit reported, want false")
	}
	if !a.trackToolCallingRound() {
		t.Fatal("round 2: silent-limit not reported, want true")
	}
}

// TestTrackToolCallingRound_TurnReasoningResetsStreak is the Issue 13
// regression test: a model that reasoned (thinking/content) in an EARLIER round
// of the same turn must NOT have its later message-less tool rounds counted as
// silent — the streak resets on turn-level reasoning, not just same-round.
func TestTrackToolCallingRound_TurnReasoningResetsStreak(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 2})

	// Round 0: model thinks (marks turn-level reasoning), then a tool call.
	a.thinkingBuf.WriteString("big reasoning block")
	a.handleThinkingDelta(provider.AssistantMessageEvent{Delta: "big reasoning block"})
	a.thinkingBuf.Reset() // buffer cleared for next round, but turn flag persists

	// Rounds 1..N: message-less tool-call-only rounds. The streak must stay 0
	// because the model reasoned earlier this turn.
	for i := 0; i < 5; i++ {
		if a.trackToolCallingRound() {
			t.Fatalf("round %d: silent-limit reported, want false (model reasoned earlier this turn)", i+1)
		}
	}
}

// TestInjectEphemeralSystemMessage_EmitsVisibleBubble verifies that recovery
// instructions remain visible to the user as system notifications.
func TestInjectEphemeralSystemMessage_EmitsVisibleBubble(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	nudge := "[goa-system] Internal control note: 15 consecutive tool-calling rounds elapsed"
	a.InjectEphemeralSystemMessage(nudge)

	events := obs.Events()
	found := false
	for _, event := range events {
		if event.Type == EventContent && event.Metadata["category"] == "system-notification" && event.Text == nudge {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected visible system notification, got %+v", events)
	}
}

// TestInjectEphemeralSystemMessage_NoBubbleForNonControl verifies that
// non-control ephemeral messages (not starting with "[goa-system]") do NOT
// emit a user-visible bubble — only host control notes need user visibility.
func TestInjectEphemeralSystemMessage_NoBubbleForNonControl(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.InjectEphemeralSystemMessage("regular ephemeral system message")

	events := obs.Events()
	for _, e := range events {
		if e.Type == EventContent && e.Metadata["category"] == "system-notification" {
			t.Fatalf("unexpected system-notification bubble for non-control message: %+v", e)
		}
	}
}

// TestMaxConsecutiveToolRounds_ZeroDisables verifies that a negative/disabled
// limit never reports the silent-limit signal.
func TestMaxConsecutiveToolRounds_ZeroDisables(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 0})

	// Run many silent rounds — the limit must never be reported.
	for i := 0; i < 20; i++ {
		if a.trackToolCallingRound() {
			t.Fatalf("round %d: silent-limit reported, want false (limit disabled)", i+1)
		}
	}
}

// TestMaxConsecutiveToolRounds_CustomThreshold verifies the silent-limit signal
// is reported at the configured threshold, not the default of 15.
func TestMaxConsecutiveToolRounds_CustomThreshold(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error), MaxConsecutiveToolRounds: 3})

	// 2 rounds — below threshold.
	if a.trackToolCallingRound() {
		t.Fatal("round 1: silent-limit reported, want false")
	}
	if a.trackToolCallingRound() {
		t.Fatal("round 2: silent-limit reported, want false")
	}

	// 3rd round — threshold reached.
	if !a.trackToolCallingRound() {
		t.Fatal("round 3: silent-limit not reported, want true (threshold 3 reached)")
	}
}
