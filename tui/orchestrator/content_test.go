// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"
)

// TestAgentContent_RendersEachTab builds a view via NEUTRAL events and asserts
// each tab kind renders the expected ANSI-stripped substrings: the Stats tab
// shows the CH column + a role, an agent tab shows the streamed text, and the
// All tab shows both roles. The no-view case returns nil.
func TestAgentContent_RendersEachTab(t *testing.T) {
	c := NewAgentContent()

	// No view attached: invisible.
	if lines := c.Render(80); lines != nil {
		t.Errorf("Render without view = %v, want nil", lines)
	}

	v := NewMultiAgentView("orchestration")
	c.SetView(v)
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted, Meta: map[string]string{"topology": "hub", "objective": "ship it"}})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "orch-1", Role: "orchestrator", Model: "qwen", Provider: "lmstudio", Thinking: "medium"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder", Model: "gemma", Provider: "google", Thinking: "off"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentMessage, AgentID: "coder-1", Role: "coder", Text: "writing tests here"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStats, AgentID: "coder-1", Role: "coder", Stats: &AgentStatsDelta{TokensIn: 40, TokensOut: 12, CacheRead: 1024}})

	// Stats tab (default active): CH header + coder role + provider column.
	stats := stripAll(c.Render(90))
	joined := strings.Join(stats, "\n")
	for _, want := range []string{"CH", "coder", "(google)", "gemma", "1024", "orchestration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("stats tab missing %q:\n%s", want, joined)
		}
	}

	// Agent tab: select coder, render its transcript.
	v.SelectByKey("coder-1")
	agent := stripAll(c.Render(90))
	joined = strings.Join(agent, "\n")
	if !strings.Contains(joined, "writing tests here") {
		t.Errorf("agent tab missing streamed text:\n%s", joined)
	}

	// All tab: both roles appear with their [role] prefix.
	v.SelectByKey("all")
	all := stripAll(c.Render(90))
	joined = strings.Join(all, "\n")
	for _, want := range []string{"[coder]", "writing tests here"} {
		if !strings.Contains(joined, want) {
			t.Errorf("all tab missing %q:\n%s", want, joined)
		}
	}
}

// TestRenderStatsTable_FormatsColumns asserts the enhanced stats table
// formats the provider/model, thinking, and cache columns exactly as the
// tabbed-run UI requires (including the "-" placeholder for zero cache).
func TestRenderStatsTable_FormatsColumns(t *testing.T) {
	rows := []AgentEnhancedRow{
		{Role: "coder", Provider: "google", Model: "gemma", Thinking: "off", TokensIn: 40, TokensOut: 12, CacheRead: 0},
		{Role: "orchestrator", Provider: "lmstudio", Model: "qwen", Thinking: "medium", CacheRead: 1024},
	}
	joined := strings.Join(stripAll(RenderStatsTable(rows, 90)), "\n")
	for _, want := range []string{"role", "CH", "(google) gemma", "(lmstudio) qwen", "1024"} {
		if !strings.Contains(joined, want) {
			t.Errorf("stats table missing %q:\n%s", want, joined)
		}
	}
}

// TestStatsTableHelpers covers the small formatting primitives in isolation.
func TestStatsTableHelpers(t *testing.T) {
	if got := providerModel("", ""); got != "-" {
		t.Errorf("providerModel empty = %q, want -", got)
	}
	if got := providerModel("", "m"); got != "m" {
		t.Errorf("providerModel model-only = %q, want m", got)
	}
	if got := stripANSI(cacheField(0)); got != "-" {
		t.Errorf("cacheField(0) = %q, want -", got)
	}
	if got := stripANSI(thinkField("")); got != "-" {
		t.Errorf("thinkField(\"\") = %q, want -", got)
	}
}

// stripAll removes ANSI escapes from every line.
func stripAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripANSI(l)
	}
	return out
}
