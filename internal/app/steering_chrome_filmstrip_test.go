// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

// TestSteeringChrome_Filmstrip_IsChromeNotTranscript validates the steering
// redesign (/quota request during streaming corrupts the TUI):
//
//   - a queued steering message renders as a pinned bottom-chrome bubble, NOT
//     as a chat transcript entry (the transcript stays append-only);
//   - the status bar is untouched by steering (no steering text/indicator in
//     the StatusMsg spinner line);
//   - when the steering is consumed, it lands in the transcript as a real user
//     message and the chrome bubble clears.
func TestSteeringChrome_Filmstrip_IsChromeNotTranscript(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// A streaming turn is in flight.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "analyzing the failing tests"})

	// The user queues a steering message mid-turn. Drive the app handler that
	// production uses (maybeSteerAgent path writes to subs.steeringChrome).
	subs := sc.app.subs
	subs.steeringChrome.Add("hold on, also check quota")
	sc.engine.ApplySync(func() {})
	sc.engine.RenderNow()

	// 1. The steering bubble is chrome: the chat transcript contains NO
	//    steering entry, while the chrome band shows the bubble.
	if text := transcriptText(sc); strings.Contains(text, "hold on, also check quota") {
		t.Errorf("steering text leaked into the chat transcript:\n%s", text)
	}
	if text := frameText(sc); !strings.Contains(text, "alt+e to edit") {
		t.Errorf("steering bubble (chrome) not visible in composed frame:\n%s", text)
	}

	// 2. The status bar is untouched: no steering text in the spinner line.
	if strings.Contains(sc.statusText(), "hold on") || strings.Contains(sc.statusText(), "quota") {
		t.Errorf("status bar must not show steering content, got %q", sc.statusText())
	}

	// 3. Consumed steering lands as a user message and the bubble clears.
	sc.engine.ApplySync(func() {
		sc.app.handleSteeringInjected(&event.SteeringInput{Text: "hold on, also check quota"})
	})
	sc.engine.RenderNow()
	if text := transcriptText(sc); !strings.Contains(text, "hold on, also check quota") {
		t.Errorf("consumed steering must land in the transcript as a user message:\n%s", text)
	}
	if text := frameText(sc); strings.Contains(text, "alt+e to edit") {
		t.Error("steering bubble must clear once the steering is consumed")
	}
	if subs.steeringChrome.HasPending() {
		t.Error("steeringChrome must report no pending steering after consumption")
	}
}

// transcriptText returns the chat transcript's text content (all entries).
func transcriptText(sc *uiScenario) string {
	var b strings.Builder
	for _, m := range sc.chat.Messages() {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// frameText returns the composed frame's visible text (ANSI-stripped).
func frameText(sc *uiScenario) string {
	return strings.Join(sc.engine.AgentFrame().Visible, "\n")
}
