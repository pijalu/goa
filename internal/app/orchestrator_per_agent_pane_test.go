// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorPerAgentPane_ContentVisibleWithLargeTranscript is the
// permanent regression gate for the "per-agent panes are empty" bug.
//
// The pre-existing TestOrchestratorPerAgentTab_ToolWidgetVisible used a tiny
// transcript, so the former monotonically-growing stable-height padding never
// ratcheted high enough to scroll the filtered content out of view — the test
// was green while reality was broken. This test uses a LARGE transcript (each
// agent produces well over a screen of content) and asserts that, after
// switching to each per-agent tab, the agent's OWN content is actually visible
// on screen (not scrolled off by stale padding, not replaced by the stats panel).
func TestOrchestratorPerAgentPane_ContentVisibleWithLargeTranscript(t *testing.T) {
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

	// Sanity: the Conversation tab holds a large transcript.
	sc.engine.ApplySync(func() { sc.app.selectAgentTab("conversation") })
	convFrame := sc.frame()
	convNode := convFrame.FindNode("ChatViewport")
	if convNode == nil || lineCount(convNode.Text) < 30 {
		t.Fatalf("conversation transcript too small: %v", convNode == nil)
	}

	// Each per-agent pane must show THAT agent's content on the visible screen.
	for _, c := range []struct{ tabKey, marker string }{
		{"orchestrator-1", "[orchestrator]"},
		{"coder-2", "[coder]"},
	} {
		sc.engine.ApplySync(func() { sc.app.selectAgentTab(c.tabKey) })
		frame := sc.frame()
		vis := strings.Join(frame.Visible, "\n")
		// AgentContent (stats) must NOT render on a per-agent tab.
		if node := frame.FindNode("orchestrator.AgentContent"); node != nil && strings.TrimSpace(node.Text) != "" {
			t.Errorf("tab %q: stats panel should be hidden on per-agent tabs", c.tabKey)
		}
		if !strings.Contains(vis, c.marker) {
			t.Errorf("tab %q: agent content %q not visible (scrolled off / blank pane);\nVISIBLE:\n%s", c.tabKey, c.marker, vis)
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