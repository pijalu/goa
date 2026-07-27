// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestStreamRetryDoesNotDuplicateAssistantText reproduces bugs.md Issue 4: when a
// stream error triggers the agent's retry path, the agent resets its contentBuf
// and re-streams the SAME response from the start, while a system-notification
// "Reconnecting…" message is shown. The chat viewport must NOT end up with the
// partial pre-retry text and the re-streamed text duplicated — the user sees
// "repeats" that shift on scroll.
func TestStreamRetryDoesNotDuplicateAssistantText(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Turn starts; assistant streams a partial answer.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	deltas := []string{"Now ", "let ", "me ", "verify ", "the ", "full ", "build."}
	for _, d := range deltas {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: d, IsDelta: true})
	}

	// Transient stream error: agent emits a retry system-notification, resets
	// contentBuf, and will re-stream from scratch.
	sc.apply(&agentic.OutputEvent{
		Type:     agentic.EventContent,
		Role:     agentic.System,
		Text:     "Connection lost; reconnecting…",
		Metadata: map[string]string{"category": "system-notification", "stream_retry": "true"},
	})

	// Retry reconnects: model re-streams the SAME content from the beginning.
	for _, d := range deltas {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: d, IsDelta: true})
	}
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Collect the whole conversation text. The answer must appear EXACTLY once —
	// no old-partial + re-streamed duplication, whether in one bubble or two.
	var sb strings.Builder
	for _, m := range sc.chat.Messages() {
		sb.WriteString(m.Content)
		sb.WriteByte('\n')
	}
	joined := sb.String()
	want := "Now let me verify the full build."
	count := strings.Count(joined, want)
	if count != 1 {
		t.Fatalf("assistant content duplicated: want %q exactly once, found %d times.\nConversation:\n%s", want, count, joined)
	}
}
