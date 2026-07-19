// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/plugins"
)

// TestFilmstrip_PluginSegmentVisibleInFooterAcrossTurn drives a full agent
// turn (thinking → tool call → tool result → end) through the app event
// handler with a plugin quota segment active, and asserts via the Filmstrip
// that the footer DOM node renders the segment in the live component tree on
// every frame. This is the event→UI validation the unit tests can't provide:
// it proves the segment survives the real render path under a moving turn.
func TestFilmstrip_PluginSegmentVisibleInFooterAcrossTurn(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Wire a plugin runtime with one rendered segment and activate it on the
	// scenario's engine, exactly as Run() does after buildTUI.
	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{
		ID:       "quota",
		Priority: 10,
		Render:   func() string { return "5h:42% / 5d:30%" },
	})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)

	// Sanity: segment pushed to footer before any event.
	sc.engine.ApplySync(func() { sc.app.pushPluginSegments(sc.engine) })
	sc.engine.RenderNow()
	preFrame := sc.engine.AgentFrame()
	footerText := preFrame.FindNode("Footer")
	if footerText == nil {
		t.Fatal("Footer node missing from AgentFrame")
	}
	if !strings.Contains(footerText.Text, "5h:42%") {
		t.Fatalf("segment not in footer before turn: %q", footerText.Text)
	}

	// Drive a realistic turn and capture a Filmstrip frame per event.
	events := []*agentic.OutputEvent{
		{Type: agentic.EventStateChange, State: agentic.StateThinking},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "reasoning…"},
		{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "read", ToolInput: `{"path":"x"}`, ToolCallID: "c1"},
		{Type: agentic.EventToolResult, State: agentic.StateToolResult, ToolName: "read", ToolCallID: "c1", Text: "file contents"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "answer"},
		{Type: agentic.EventEnd},
	}
	for _, ev := range events {
		sc.apply(ev)
	}

	// Across every captured frame, the footer must keep showing the segment —
	// the streaming/activity churn must not blank it.
	frames := sc.film.Frames()
	if len(frames) != len(events) {
		t.Fatalf("captured %d frames, want %d", len(frames), len(events))
	}
	for i, fr := range frames {
		node := fr.Frame.FindNode("Footer")
		if node == nil {
			t.Errorf("frame %d (%s): Footer node missing", i, fr.Label)
			continue
		}
		if !strings.Contains(node.Text, "5h:42% / 5d:30%") {
			t.Errorf("frame %d (%s): segment lost mid-turn; footer=%q", i, fr.Label, node.Text)
		}
	}
}

// TestFilmstrip_PluginSegmentRefreshUpdatesFooter validates the refresh path:
// a plugin changing its cached value and calling goa.ui.refreshSegment must
// surface the new text in the footer on the next frame, without any agent
// event. This is the carousel-rotation mechanism.
func TestFilmstrip_PluginSegmentRefreshUpdatesFooter(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// A segment whose rendered text the test controls (simulating the JS
	// carousel advancing its index).
	current := "5h:42% / 5d:30%"
	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{
		ID:       "quota",
		Priority: 10,
		Render:   func() string { return current },
	})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)

	read := func() string {
		sc.engine.ApplySync(func() { sc.app.pushPluginSegments(sc.engine) })
		sc.engine.RenderNow()
		frame := sc.engine.AgentFrame()
		node := frame.FindNode("Footer")
		if node == nil {
			return ""
		}
		return node.Text
	}

	if got := read(); !strings.Contains(got, "5h:42%") {
		t.Fatalf("initial segment missing: %q", got)
	}

	// Carousel advances to the next provider.
	current = "Z.ai 5h:15% / 5d:8%"
	if got := read(); !strings.Contains(got, "Z.ai 5h:15%") {
		t.Fatalf("refreshed segment missing: %q", got)
	}
	if strings.Contains(read(), "5h:42%") {
		t.Fatalf("stale segment still shown after refresh: %q", read())
	}
}

// TestFilmstrip_PluginSegmentSurvivesStatsUpdate ensures routine token-stats
// footer updates (which rebuild FooterData with PluginSegments nil) don't
// blank the segment — the preserve logic must carry it across the churn of a
// streaming turn.
func TestFilmstrip_PluginSegmentSurvivesStatsUpdate(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{
		ID:       "quota",
		Priority: 10,
		Render:   func() string { return "5h:42%" },
	})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)
	sc.engine.ApplySync(func() { sc.app.pushPluginSegments(sc.engine) })

	// Token-stats events fire constantly during streaming and rebuild the
	// footer data. The segment must persist through them.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventTokenStats})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContextStats})

	statsFrame := sc.engine.AgentFrame()
	node := statsFrame.FindNode("Footer")
	if node == nil || !strings.Contains(node.Text, "5h:42%") {
		t.Fatalf("segment lost after stats updates: %v", node)
	}
}

// TestFilmstrip_NoPluginRuntimeNoSegment pins the baseline: without an active
// plugin runtime, the footer shows no segment bullet (guard against the
// segment pipeline leaking into the default UI).
func TestFilmstrip_NoPluginRuntimeNoSegment(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	noRtFrame := sc.engine.AgentFrame()
	node := noRtFrame.FindNode("Footer")
	if node != nil && strings.Contains(node.Text, "5h:") {
		t.Fatalf("segment leaked into footer without plugin runtime: %q", node.Text)
	}
}

// TestFilmstrip_ProviderSwitchRefreshesSegment covers the live bug "switching
// to a local provider still shows the previous provider's quota": the footer
// segment is a cached string, so changing cfg.ActiveProvider must re-evaluate
// the plugin segment render — not wait for the plugin's periodic refresh.
// refreshFooterFromConfig (fired by /model's FooterRefresh) must re-push
// segments even though the plugin never calls goa.ui.refreshSegment.
func TestFilmstrip_ProviderSwitchRefreshesSegment(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Segment text derives from the active provider, like the quota plugin.
	rt := newPluginRuntime(sc.app.subs)
	rt.ui.AddSegment(plugins.UISegmentDef{
		ID:       "quota",
		Priority: 10,
		Render: func() string {
			if sc.app.subs.cfg.ActiveProvider == "lmstudio" {
				return "[∞]"
			}
			return "[9%|30%]"
		},
	})
	sc.app.subs.setPluginRT(rt)
	sc.app.activatePluginUI(sc.engine)

	read := func() string {
		sc.engine.RenderNow()
		frame := sc.engine.AgentFrame()
		if node := frame.FindNode("Footer"); node != nil {
			return node.Text
		}
		return ""
	}

	sc.app.subs.cfg.ActiveProvider = "kimi-code"
	sc.engine.ApplySync(func() { sc.app.pushPluginSegments(sc.engine) })
	if got := read(); !strings.Contains(got, "9%|30%") {
		t.Fatalf("initial kimi quota segment missing: %q", got)
	}

	// Switch to a local provider via the /model path (config change + footer
	// refresh event), WITHOUT any plugin refreshSegment call.
	sc.app.subs.cfg.ActiveProvider = "lmstudio"
	sc.engine.ApplySync(func() { sc.app.refreshFooterFromConfig() })

	got := read()
	if !strings.Contains(got, "[∞]") {
		t.Fatalf("after provider switch the segment must show the local provider, got: %q", got)
	}
	if strings.Contains(got, "9%|30%") {
		t.Fatalf("stale kimi quota still shown after provider switch: %q", got)
	}
}
