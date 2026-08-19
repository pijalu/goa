// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// newFreshWindowAgent builds an agent with the fresh_window gate ON and a
// summarize probe provider, so tests can prove the window reset never calls
// the model. Remote compaction stays unavailable (the default off gate), so
// the fresh window is the selected full-compaction strategy.
func newFreshWindowAgent(p *provenanceSummarizeProvider, history []Message) *Agent {
	agent := newProvenanceAgent(p, history)
	agent.cfg.ContextCompression.FreshWindow.Enabled = true
	return agent
}

// TestCompactFreshWindow_ZeroLLM is the core 2b.3 acceptance test: with the
// fresh_window strategy configured, Compact installs a fresh context window
// with ZERO summarization calls — no summarize stream, no remote compact
// call — while emitting the full compaction lifecycle (provenance triple +
// EventCompact labelled fresh_window), advancing the cache generation, and
// rotating the provider cache key.
func TestCompactFreshWindow_ZeroLLM(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-probe-%d", testProviderCounter.Add(1))),
		reply: "summarize must never run",
		usage: &provider.Usage{InputTokens: 1, OutputTokens: 1},
	}
	agent := newFreshWindowAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	genBefore := agent.cacheGeneration
	keyBefore := agent.cacheKey(agent.cfg.Model)
	before := agent.ContextStats()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Zero LLM calls: no summarize stream was opened.
	if p.callCount() != 0 {
		t.Errorf("fresh_window must make no LLM calls; summarize stream count = %d", p.callCount())
	}
	assertFreshWindowTail(t, agent, 5)
	assertFreshWindowKeyRotation(t, agent, genBefore, keyBefore)
	assertFreshWindowProvenance(t, obs, before, agent.ContextStats())
}

// assertFreshWindowTail checks the landed window reset: the system message
// plus the preserved tail (default preserve=2 → the last two user/assistant
// turns), ending on the newest assistant reply.
func assertFreshWindowTail(t *testing.T, agent *Agent, wantLen int) {
	t.Helper()
	agent.mu.Lock()
	got := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(got) != wantLen {
		t.Fatalf("fresh window history = %d messages, want %d (system + preserved turns): %#v", len(got), wantLen, got)
	}
	if got[0].Role != System {
		t.Errorf("history[0].Role = %q, want the preserved system message", got[0].Role)
	}
	if got[len(got)-1].Role != Assistant {
		t.Errorf("tail ends with role %q, want the last assistant reply preserved", got[len(got)-1].Role)
	}
}

// assertFreshWindowKeyRotation checks the generation advance and cache-key
// rotation (explicit compaction boundary, invariant 1 / Hard Rule 7).
func assertFreshWindowKeyRotation(t *testing.T, agent *Agent, genBefore uint64, keyBefore string) {
	t.Helper()
	if agent.cacheGeneration != genBefore+1 {
		t.Errorf("cacheGeneration = %d, want %d", agent.cacheGeneration, genBefore+1)
	}
	if keyAfter := agent.cacheKey(agent.cfg.Model); keyAfter == keyBefore {
		t.Error("cache key must rotate after the fresh-window reset (non-prefix history change)")
	}
}

// assertFreshWindowProvenance checks the lifecycle contract: the provenance
// triple fires in order with a shared id, a nil Usage (zero-LLM — there is no
// summarize call to report), the whole pre-reset history shadowed, and a
// positive freed figure; the surviving EventCompact carries the fresh_window
// strategy label.
func assertFreshWindowProvenance(t *testing.T, obs *mockEventObserver, before, after ContextStats) {
	t.Helper()
	txs := compactionTxEvents(obs)
	if len(txs) != 3 {
		t.Fatalf("expected the provenance triple, got %d events: %v", len(txs), obs.Events())
	}
	assertProvenanceTripleOrder(t, txs)
	assertProvenanceSharedID(t, txs)
	assertProvenanceCleanEnd(t, txs[2].CompactionTx)
	sum := txs[1].CompactionTx
	if sum.Usage != nil {
		t.Errorf("fresh_window compaction_summary Usage = %+v, want nil (no LLM call was made)", sum.Usage)
	}
	if sum.ShadowedStart != 0 || sum.ShadowedEnd != 9 || sum.ShadowedCount != 9 {
		t.Errorf("shadowed range = [%d,%d) count=%d, want [0,9) count=9 (the whole pre-reset history)",
			sum.ShadowedStart, sum.ShadowedEnd, sum.ShadowedCount)
	}
	if wantFreed := before.EstimatedTokens - after.EstimatedTokens; sum.FreedTokens != wantFreed {
		t.Errorf("FreedTokens = %d, want the measured before−after delta %d", sum.FreedTokens, wantFreed)
	}
	if sum.FreedTokens <= 0 {
		t.Errorf("FreedTokens = %d, want > 0 (the window reset frees the shadowed span)", sum.FreedTokens)
	}
	evs := compactEvents(obs)
	if len(evs) != 1 || evs[0].Compaction == nil || evs[0].Compaction.Strategy != string(CompressionFreshWindow) {
		t.Errorf("EventCompact = %+v, want exactly one fresh_window compaction event", evs)
	}
}

// TestCompactFreshWindow_PreservesConfiguredTurns verifies the preservation
// tail is configurable and the last user message always survives: with
// FreshWindow.PreserveRecentTurns=1 only the final turn is kept, and the
// reset still ends on the assistant reply so the next user input alternates
// correctly.
func TestCompactFreshWindow_PreservesConfiguredTurns(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-preserve-probe-%d", testProviderCounter.Add(1))),
		reply: "no summarize",
	}
	agent := newFreshWindowAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.FreshWindow.PreserveRecentTurns = 1

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	got := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	// system + 1 user + 1 assistant.
	if len(got) != 3 {
		t.Fatalf("history = %d messages, want 3 (system + last preserved turn): %#v", len(got), got)
	}
	if got[1].Role != User || got[2].Role != Assistant {
		t.Errorf("preserved turn roles = [%q %q], want [user assistant]", got[1].Role, got[2].Role)
	}
}

// TestCompactFreshWindow_ChainSafeToolCalls verifies the reset never splits a
// tool-call chain: the boundary widens so a preserved tool result keeps its
// assistant tool-call message (strict providers reject a leading tool result
// with no preceding tool_calls).
func TestCompactFreshWindow_ChainSafeToolCalls(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-chain-probe-%d", testProviderCounter.Add(1))),
		reply: "no summarize",
	}
	history := []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "old turn"},
		{Type: Content, Role: Assistant, Content: "old reply"},
		{Type: Content, Role: User, Content: "recent turn"},
		{Type: Content, Role: Assistant, Content: "", ToolCalls: []ToolCallInfo{{ID: "c1", Type: "function", Name: "read_file", Arguments: "{}"}}},
		{Type: Content, Role: ToolRole, Content: "file body", ToolCallID: "c1", ToolName: "read_file"},
		{Type: Content, Role: Assistant, Content: "final reply"},
	}
	agent := newFreshWindowAgent(p, history)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	got := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	// The preserved tail must start at the user turn and keep the whole
	// assistant→tool chain; no leading orphaned tool result.
	for i, m := range got {
		if m.Role == ToolRole {
			if i == 0 || got[i-1].Role != Assistant || len(got[i-1].ToolCalls) == 0 {
				t.Fatalf("orphaned tool result at index %d: %#v", i, got)
			}
		}
	}
	if got[0].Role != System {
		t.Errorf("history[0].Role = %q, want system preserved", got[0].Role)
	}
}

// TestCompactFreshWindow_RemoteWins verifies the formal ordering: when remote
// compaction is available it runs FIRST, even with fresh_window configured —
// the fresh window never fires and the remote replacement lands.
func TestCompactFreshWindow_RemoteWins(t *testing.T) {
	p := &remoteCompactProbeProvider{
		api:            provider.Api(fmt.Sprintf("fresh-window-remote-probe-%d", testProviderCounter.Add(1))),
		replacement:    remoteReplacement(),
		summarizeReply: "no summarize",
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.FreshWindow.Enabled = true

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.compactCallCount() != 1 {
		t.Errorf("remote compact must run once when available, got %d", p.compactCallCount())
	}
	if p.streamCallCount() != 0 {
		t.Errorf("no local call must run, stream count = %d", p.streamCallCount())
	}
	// The REMOTE replacement landed (2 messages), not the fresh-window tail
	// (which would be system + 4).
	if len(agent.history) != 2 {
		t.Errorf("history = %d messages, want the remote replacement (2), not the fresh-window tail", len(agent.history))
	}
}

// TestCompactFreshWindow_BeatsLocalSummarize verifies the middle rung: with
// remote unavailable and fresh_window configured, the zero-LLM reset beats
// the local summarize ladder.
func TestCompactFreshWindow_BeatsLocalSummarize(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-order-probe-%d", testProviderCounter.Add(1))),
		reply: "summarize must not run",
	}
	agent := newFreshWindowAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.callCount() != 0 {
		t.Errorf("local summarize must not run when fresh_window is configured, calls = %d", p.callCount())
	}
	evs := compactEvents(obs)
	if len(evs) != 1 || evs[0].Compaction.Strategy != string(CompressionFreshWindow) {
		t.Errorf("EventCompact strategy = %+v, want fresh_window", evs)
	}
}

// TestCompactFreshWindow_SelectedViaStrategySlot verifies config-selectability
// WITHOUT the enabled gate: naming fresh_window on the hard-layer strategy
// slot selects it (selection implies intent), and the compact still routes
// through the ordering.
func TestCompactFreshWindow_SelectedViaStrategySlot(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-slot-probe-%d", testProviderCounter.Add(1))),
		reply: "summarize must not run",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.Strategies.Hard = CompressionFreshWindow

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.callCount() != 0 {
		t.Errorf("hard=fresh_window must select the zero-LLM reset without the gate; summarize calls = %d", p.callCount())
	}
	// Fresh-window tail landed: system + 2 preserved turns.
	if len(agent.history) != 5 {
		t.Errorf("history = %d messages, want the fresh-window tail (5)", len(agent.history))
	}
}

// TestCompactFreshWindow_StrategyDispatch verifies a per-layer selection of
// fresh_window routes through the strategy dispatch (compressHistoryWith) and
// still performs the zero-LLM reset.
func TestCompactFreshWindow_StrategyDispatch(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-dispatch-probe-%d", testProviderCounter.Add(1))),
		reply: "summarize must not run",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.MaybeCompressWith(context.Background(), CompressionFreshWindow, true); err != nil {
		t.Fatalf("MaybeCompressWith(fresh_window): %v", err)
	}
	if p.callCount() != 0 {
		t.Errorf("dispatch must run the zero-LLM reset, summarize calls = %d", p.callCount())
	}
	evs := compactEvents(obs)
	if len(evs) != 1 || evs[0].Compaction.Strategy != string(CompressionFreshWindow) {
		t.Errorf("EventCompact strategy = %+v, want fresh_window", evs)
	}
}

// TestCompact_LocalLadderUnchangedWithoutFreshWindow pins the fallback
// contract: with neither remote compaction nor fresh_window configured,
// Compact still runs the local summarize ladder exactly as before 2b.3.
func TestCompact_LocalLadderUnchangedWithoutFreshWindow(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("fresh-window-off-probe-%d", testProviderCounter.Add(1))),
		reply: "## Primary Request and Intent\n- local summary",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.callCount() != 1 {
		t.Errorf("local summarize must run once when fresh_window is not configured, calls = %d", p.callCount())
	}
	// The summarize checkpoint pair landed.
	if len(agent.history) != 2 || !strings.Contains(agent.history[0].Content, compactSummaryOpenTag) {
		t.Errorf("history = %#v, want the local summarize checkpoint pair", agent.history)
	}
	evs := compactEvents(obs)
	if len(evs) != 1 || evs[0].Compaction.Strategy != string(CompressionSummarize) {
		t.Errorf("EventCompact strategy = %+v, want summarize (the local ladder is unchanged)", evs)
	}
}

// TestFreshWindowSelected covers the pure selection primitive: the gate, slot
// selection, and the zero-value default (local ladder unchanged).
func TestFreshWindowSelected(t *testing.T) {
	a := &Agent{}
	// Zero value: no gate, no slot → local ladder.
	if a.freshWindowSelected(CompressionSummarize) {
		t.Error("zero config must not select fresh_window for the summarize slot")
	}
	if a.freshWindowSelected("") {
		t.Error("zero config must not select fresh_window for an empty slot")
	}

	// Slot selection alone implies intent.
	if !a.freshWindowSelected(CompressionFreshWindow) {
		t.Error("a fresh_window slot must select the strategy without the gate")
	}

	// The gate upgrades every slot.
	a.cfg.ContextCompression.FreshWindow.Enabled = true
	if !a.freshWindowSelected(CompressionSummarize) {
		t.Error("the enabled gate must upgrade the summarize slot")
	}
	if !a.freshWindowSelected(CompressionHybrid) {
		t.Error("the enabled gate must upgrade any slot")
	}
}

// TestFreshWindowPreserveTurns covers the preservation-length resolution:
// the fresh-window override wins, then the shared PreserveRecentTurns, then
// the engine default, and the result never drops below 1 (the last user
// message always survives).
func TestFreshWindowPreserveTurns(t *testing.T) {
	a := &Agent{}
	if got := a.freshWindowPreserveTurns(); got != 2 {
		t.Errorf("zero config preserve = %d, want the engine default 2", got)
	}
	a.cfg.ContextCompression.PreserveRecentTurns = 4
	if got := a.freshWindowPreserveTurns(); got != 4 {
		t.Errorf("preserve = %d, want the shared PreserveRecentTurns 4", got)
	}
	a.cfg.ContextCompression.FreshWindow.PreserveRecentTurns = 1
	if got := a.freshWindowPreserveTurns(); got != 1 {
		t.Errorf("preserve = %d, want the fresh-window override 1", got)
	}
	// A negative override is clamped to inherit (the shared value).
	a.cfg.ContextCompression.FreshWindow.PreserveRecentTurns = -3
	if got := a.freshWindowPreserveTurns(); got != 4 {
		t.Errorf("preserve = %d, want the clamped inherit 4", got)
	}
}
