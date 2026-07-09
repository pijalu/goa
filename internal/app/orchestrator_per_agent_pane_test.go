// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorConversation_LargeTranscriptVisible is the regression gate
// for the unified conversation view. Per-agent tabs were removed, so all
// agent content (including tool widgets and source tags) must remain visible in
// the single conversation view even when the transcript is larger than the
// screen.
func TestOrchestratorConversation_LargeTranscriptVisible(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	// Build a large, realistic hub-delegation transcript. Each agent emits
	// enough content to overflow the conversation budget on its own.
	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "build flame sim", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "orchestrator-1", Role: "orchestrator", Model: "gemma",
			Payload: map[string]any{"provider": "lmstudio", "thinking": "high"}},
	}
	// Orchestrator: lots of thinking + a message + a delegate tool call.
	events = append(events, bigThinking("orchestrator-1", "orchestrator", "plan the flame simulation step by step")...)
	events = append(events, bigMessages("orchestrator-1", "orchestrator", "[orchestrator] synthesis summary line")...)
	events = append(events, orchestrator.Event{Type: orchestrator.EventAgentToolCall, AgentID: "orchestrator-1", Role: "orchestrator",
		Payload: map[string]any{"tool": "delegate", "input": `{"role":"coder","task":"write files"}`, "call_id": "d1"}})

	// Coder: started, thinking, a write tool call + result, lots of message.
	events = append(events, orchestrator.Event{Type: orchestrator.EventAgentStarted, AgentID: "coder-2", Role: "coder", Model: "gemma",
		Payload: map[string]any{"provider": "lmstudio", "thinking": "high"}})
	events = append(events, bigThinking("coder-2", "coder", "implement the particle system")...)
	events = append(events, orchestrator.Event{Type: orchestrator.EventAgentToolCall, AgentID: "coder-2", Role: "coder",
		Payload: map[string]any{"tool": "write", "input": `{"path":"index.html"}`, "call_id": "w1"}})
	events = append(events, orchestrator.Event{Type: orchestrator.EventAgentToolResult, AgentID: "coder-2", Role: "coder",
		Payload: map[string]any{"call_id": "w1", "text": "[write: index.html]\nwrote 672 bytes", "ok": true}})
	events = append(events, bigMessages("coder-2", "coder", "[coder] final report line")...)

	// Route every event through the real forwarder path.
	for _, ev := range events {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		nev := ne
		sc.engine.ApplySync(func() { sc.app.handleOrchViewEvent(nev) })
	}
	sc.flush()

	// Sanity: only the two bookend tabs exist.
	sc.engine.ApplySync(func() { sc.app.selectAgentTab("conversation") })
	view := sc.app.subs.agentView
	if view == nil {
		t.Fatal("agentView missing")
	}
	keys := make([]string, len(view.Tabs()))
	for i, tab := range view.Tabs() {
		keys[i] = tab.Key
	}
	if len(keys) != 2 {
		t.Errorf("tabs = %v, want [stats conversation]", keys)
	}

	// The conversation view must hold a large transcript and show both agents.
	convFrame := sc.frame()
	convNode := convFrame.FindNode("ChatViewport")
	if convNode == nil || lineCount(convNode.Text) < 30 {
		t.Fatalf("conversation transcript too small: %v", convNode == nil)
	}
	for _, marker := range []string{"[orchestrator]", "[coder]", "delegate", "write"} {
		if !strings.Contains(convNode.Text, marker) {
			t.Errorf("conversation view missing marker %q", marker)
		}
	}

	// No per-agent tab should exist for either agent.
	for _, key := range []string{"orchestrator-1", "coder-2"} {
		sc.engine.ApplySync(func() { sc.app.selectAgentTab(key) })
		if tab, _ := sc.app.subs.agentView.ActiveTab(); tab.Key == key {
			t.Errorf("per-agent tab %q should not exist", key)
		}
	}
}

// bigThinking emits N agent_thinking events whose combined text spans many
// lines, so the thinking block overflows a screen.
func bigThinking(id, role, prefix string) []orchestrator.Event {
	var out []orchestrator.Event
	for i := 0; i < 12; i++ {
		out = append(out, orchestrator.Event{
			Type: orchestrator.EventAgentThinking, AgentID: id, Role: role,
			Payload: map[string]any{"text": prefix + " reasoning step number " + itoa(i) + "\n"},
		})
	}
	return out
}

// bigMessages emits agent_message events building a multi-line message.
func bigMessages(id, role, prefix string) []orchestrator.Event {
	var out []orchestrator.Event
	for i := 0; i < 12; i++ {
		out = append(out, orchestrator.Event{
			Type: orchestrator.EventAgentMessage, AgentID: id, Role: role,
			Payload: map[string]any{"text": prefix + " message line " + itoa(i) + "\n"},
		})
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
