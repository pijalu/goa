// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestAgentContent_RendersTabs builds a view via NEUTRAL events and asserts
// the Conversation tab returns nil (chat renders the conversation) and the
// Stats tab renders the CH column + a role. The no-view case returns nil.
func TestAgentContent_RendersTabs(t *testing.T) {
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
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStats, AgentID: "coder-1", Role: "coder", Stats: &AgentStatsDelta{TokensIn: 40, TokensOut: 12, CacheRead: 1024}})

	// Stats tab (default active): content component renders the stats panel.
	if lines := c.Render(90); lines == nil {
		t.Fatal("stats tab should render content")
	}

	// Conversation tab: content component returns nil because the chat
	// viewport renders the conversation.
	v.SelectByKey("conversation")
	if lines := c.Render(90); lines != nil {
		t.Errorf("conversation tab should render nil, got %v", lines)
	}

	// Back to Stats tab: CH header + coder role + provider column.
	v.SelectByKey("stats")
	stats := stripAll(c.Render(90))
	joined := strings.Join(stats, "\n")
	for _, want := range []string{"CH", "coder", "(google)", "gemma", "96%", "orchestration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("stats tab missing %q:\n%s", want, joined)
		}
	}
}

// TestAgentContent_ShowsNavHint asserts the Stats tab renders the navigation
// hint so users can discover Ctrl+x without reading docs.
func TestAgentContent_ShowsNavHint(t *testing.T) {
	c := NewAgentContent()
	v := NewMultiAgentView("orchestration")
	c.SetView(v)
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "c-1", Role: "coder"})
	v.SelectByKey("stats")

	statsHint := strings.Join(stripAll(c.Render(80)), "\n")
	if !strings.Contains(statsHint, "Ctrl+x tabs") {
		t.Errorf("stats tab missing nav hint:\n%s", statsHint)
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
	for _, want := range []string{"role", "CH", "(google) gemma", "(lmstudio) qwen", "100%"} {
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
	if got := stripANSI(cacheField(0, 0, 0)); got != "-" {
		t.Errorf("cacheField(0,0,0) = %q, want -", got)
	}
	// Reads + writes (Anthropic-style): 300/(300+100) = 75%.
	if got := stripANSI(cacheField(300, 100, 0)); got != "75%" {
		t.Errorf("cacheField(300,100,0) = %q, want 75%%", got)
	}
	// Reads only with net prompt (OpenAI-style): 500/(500+500) = 50%.
	if got := stripANSI(cacheField(500, 0, 500)); got != "50%" {
		t.Errorf("cacheField(500,0,500) = %q, want 50%%", got)
	}
	// Writes but no reads → genuine 0%, not the placeholder "-".
	if got := stripANSI(cacheField(0, 100, 400)); got != "0%" {
		t.Errorf("cacheField(0,100,400) = %q, want 0%%", got)
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

// TestRenderStatsTable_CacheHitPercentage verifies the CH column renders a
// cache-hit percentage (not a raw token count), including the "-" placeholder
// for agents with no cache activity and a genuine "0%" for writes-without-reads.
func TestRenderStatsTable_CacheHitPercentage(t *testing.T) {
	rows := []AgentEnhancedRow{
		{Role: "coder", TokensIn: 400, CacheRead: 500, CacheCreation: 100}, // 500/(500+100)=83%
		{Role: "reviewer", TokensIn: 100, CacheRead: 0, CacheCreation: 50}, // 0% (writes, no reads)
		{Role: "writer", TokensIn: 50, CacheRead: 0, CacheCreation: 0},    // no activity → "-"
	}
	joined := strings.Join(stripAll(RenderStatsTable(rows, 90)), "\n")
	for _, want := range []string{"83%", "0%", "-"} {
		if !strings.Contains(joined, want) {
			t.Errorf("stats table missing %q:\n%s", want, joined)
		}
	}
	// The raw cache-read token count (500) must no longer leak into the CH
	// column now that it renders as a percentage.
	if strings.Contains(joined, "500") {
		t.Errorf("cache column should show percentages, not raw count 500; got:\n%s", joined)
	}
}

// TestAgentContent_HeaderPaddedToWidth verifies every Stats-tab line is padded
// to exactly the requested width (so the panel background spans the full width)
// and the aggregate footer carries a non-zero cache-hit percentage.
func TestAgentContent_HeaderPaddedToWidth(t *testing.T) {
	c := NewAgentContent()
	v := NewMultiAgentView("orchestration")
	c.SetView(v)
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted, Meta: map[string]string{"topology": "hub"}})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStats, AgentID: "c-1", Role: "coder",
		Stats: &AgentStatsDelta{TokensIn: 400, CacheRead: 500, CacheCreation: 100}})
	v.SelectByKey("stats")

	const w = 90
	lines := c.Render(w)
	if len(lines) == 0 {
		t.Fatal("expected stats lines")
	}
	for i, line := range lines {
		if vl := visibleLen(line); vl != w {
			t.Errorf("line %d visible width = %d, want %d: %q", i, vl, w, stripANSI(line))
		}
	}
	footer := ""
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "Σ in=") {
			footer = stripANSI(l)
			break
		}
	}
	if footer == "" {
		t.Fatalf("footer (Σ in=) not found in stats output: %v", lines)
	}
	if !strings.Contains(footer, "CH=") || !strings.Contains(footer, "%") {
		t.Errorf("footer should show CH=<pct>%%; got %q", footer)
	}
	// Aggregate: 500 reads / (500+100) writes = 83%.
	if !strings.Contains(footer, "83%") {
		t.Errorf("footer aggregate cache-hit should be 83%%; got %q", footer)
	}
}

// TestStatsTable_TruncFieldUsesVisibleWidth verifies truncField measures width
// with visibleLen (ANSI-aware, rune-aware) so ANSI-bearing or multi-byte strings
// whose byte length exceeds n but whose visible width fits are not truncated.
func TestStatsTable_TruncFieldUsesVisibleWidth(t *testing.T) {
	// ANSI-colored "code": visible width 4, byte length > 13 due to escapes.
	colored := ansi.Fg(colSuccess) + "code" + ansi.Reset
	if got := stripANSI(truncField(colored, 13)); got != "code" {
		t.Errorf("truncField(colored, 13) visible = %q, want 'code' (must not truncate)", got)
	}
	// Plain ASCII that fits is unchanged.
	if truncField("hello", 13) != "hello" {
		t.Errorf("truncField altered a fitting ASCII string")
	}
	// Too-long ASCII truncates to n-1 visible columns + ellipsis.
	if got := stripANSI(truncField("abcdefghijklm", 8)); got != "abcdefg…" {
		t.Errorf("truncField(long, 8) = %q, want 'abcdefg…'", got)
	}
	// Multi-byte: 6 runes, 18 bytes, visible width 6 — fits in 13, must not truncate.
	mb := "编码器测试数据"
	if truncField(mb, 13) != mb {
		t.Errorf("truncField(multibyte, 13) altered a fitting multibyte string: %q", truncField(mb, 13))
	}
}
