// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// --- Pre-compaction tool-result pruner (CX1) tests ---

// bigToolPair appends one assistant tool call plus its tool result of the
// given rune size — the session shape the pruner targets.
func bigToolPair(a *Agent, id string, resultRunes int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history,
		Message{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{{ID: id, Type: "function", Name: "tool", Arguments: `{"n":1}`}}},
		Message{Type: Content, Role: ToolRole, Content: strings.Repeat("x", resultRunes), ToolCallID: id, ToolName: "tool"},
	)
}

// TestPruneToolResultContent covers the pure pruning function: over-budget
// content is rewritten to head + PruneMarker + tail (runes), within-threshold
// content is left alone, and the emitted result satisfies the dsh invariants
// (≤ threshold and strictly smaller than the triggering input).
func TestPruneToolResultContent(t *testing.T) {
	cfg := DefaultToolResultPruningConfig
	emitted := cfg.HeadChars + len([]rune(PruneMarker)) + cfg.TailChars

	tests := []struct {
		name      string
		content   string
		wantPrune bool
	}{
		{name: "over-budget ASCII", content: strings.Repeat("a", 20000), wantPrune: true},
		{name: "within threshold untouched", content: strings.Repeat("b", cfg.ThresholdChars), wantPrune: false},
		// 9000 CJK runes: 27000 bytes but 9000 code points — over the 8192
		// code-point threshold, so it must prune without splitting a rune.
		{name: "unicode slices by code point", content: strings.Repeat("界", 9000), wantPrune: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pruned, ok := pruneToolResultContent(tt.content, cfg)
			if ok != tt.wantPrune {
				t.Fatalf("pruned = %v, want %v", ok, tt.wantPrune)
			}
			if !ok {
				return
			}
			assertPrunedShape(t, pruned, tt.content, cfg, emitted)
		})
	}
}

// assertPrunedShape asserts the CX1 invariants on a pruned result: ≤
// threshold runes, strictly smaller than the original, exact
// head+marker+tail rune budget, and head/tail retained by code point.
func assertPrunedShape(t *testing.T, pruned, original string, cfg ToolResultPruningConfig, emitted int) {
	t.Helper()
	prunedRunes := len([]rune(pruned))
	if prunedRunes > cfg.ThresholdChars {
		t.Errorf("pruned result %d runes exceeds threshold %d", prunedRunes, cfg.ThresholdChars)
	}
	if prunedRunes >= len([]rune(original)) {
		t.Errorf("pruned result %d runes not smaller than original %d", prunedRunes, len([]rune(original)))
	}
	if !strings.Contains(pruned, PruneMarker) {
		t.Errorf("pruned result missing marker %q", PruneMarker)
	}
	if prunedRunes != emitted {
		t.Errorf("pruned size = %d runes, want exactly %d (head+marker+tail)", prunedRunes, emitted)
	}
	headRune := []rune(original)[0]
	if !strings.HasPrefix(pruned, strings.Repeat(string(headRune), cfg.HeadChars)) {
		t.Error("head not retained by code point")
	}
	if !strings.HasSuffix(pruned, strings.Repeat(string(headRune), cfg.TailChars)) {
		t.Error("tail not retained by code point")
	}
}

// TestPruneToolResultContent_Idempotent is the CX1 idempotency acceptance:
// a second pass over an already-pruned result emits nothing.
func TestPruneToolResultContent_Idempotent(t *testing.T) {
	cfg := DefaultToolResultPruningConfig
	original := strings.Repeat("z", 50000)
	pruned, ok := pruneToolResultContent(original, cfg)
	if !ok {
		t.Fatal("first pass did not prune")
	}
	if _, ok := pruneToolResultContent(pruned, cfg); ok {
		t.Error("second pass pruned an already-pruned result — pass is not idempotent")
	}
}

// TestAgent_PruneToolResultsLocked checks the in-history pass: only ToolRole
// messages are rewritten, ToolCallID/ToolName pairing fields are untouched,
// and a second locked pass changes nothing (idempotent).
func TestAgent_PruneToolResultsLocked(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "start"},
		{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{{ID: "c1", Type: "function", Name: "bash", Arguments: `{"cmd":"ls"}`}}},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("y", 20000), ToolCallID: "c1", ToolName: "bash"},
		{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{{ID: "c2", Type: "function", Name: "read", Arguments: `{"p":"f"}`}}},
		{Type: Content, Role: ToolRole, Content: "small result", ToolCallID: "c2", ToolName: "read"},
		{Type: Content, Role: Assistant, Content: "done"},
	})

	cfg := DefaultToolResultPruningConfig
	agent.mu.Lock()
	changed := agent.pruneToolResultsLocked(cfg)
	agent.mu.Unlock()
	if changed != 1 {
		t.Fatalf("pruned %d results, want 1 (only the over-budget one)", changed)
	}

	assertPrunedToolResult(t, agent.GetHistory(), "c1", cfg)
	assertToolResultContent(t, agent.GetHistory(), "c2", "small result")

	// Idempotency at the pass level.
	agent.mu.Lock()
	second := agent.pruneToolResultsLocked(cfg)
	agent.mu.Unlock()
	if second != 0 {
		t.Errorf("second pass pruned %d results, want 0 (idempotent)", second)
	}
}

// assertPrunedToolResult asserts the tool result for callID was pruned within
// threshold, carries the marker, and kept its pairing fields (callId/name).
func assertPrunedToolResult(t *testing.T, hist []Message, callID string, cfg ToolResultPruningConfig) {
	t.Helper()
	m := findToolResult(t, hist, callID)
	if m.ToolName == "" {
		t.Errorf("tool result %q lost its ToolName", callID)
	}
	if got := len([]rune(m.Content)); got > cfg.ThresholdChars {
		t.Errorf("pruned content %d runes exceeds threshold %d", got, cfg.ThresholdChars)
	}
	if !strings.Contains(m.Content, PruneMarker) {
		t.Errorf("pruned content for %q missing the prune marker", callID)
	}
}

// assertToolResultContent asserts the tool result for callID is verbatim.
func assertToolResultContent(t *testing.T, hist []Message, callID, want string) {
	t.Helper()
	if m := findToolResult(t, hist, callID); m.Content != want {
		t.Errorf("tool result %q = %q, want verbatim %q", callID, m.Content, want)
	}
}

// findToolResult returns the ToolRole message for callID, failing when absent
// or when the pairing field was wiped.
func findToolResult(t *testing.T, hist []Message, callID string) *Message {
	t.Helper()
	for i := range hist {
		m := &hist[i]
		if m.Role == ToolRole && m.ToolCallID == callID {
			return m
		}
		if m.Role == ToolRole && m.ToolCallID == "" {
			t.Errorf("tool result at index %d lost its ToolCallID", i)
		}
	}
	t.Fatalf("tool result for %q not found", callID)
	return nil
}

// TestAgent_CompactSkipsSummarizeWhenPruningResolves is the CX1 re-measure
// acceptance — for the OPT-IN path: with pre-pruning enabled, pruning drops
// the estimate under the escalation level, so Compact skips the summarize LLM
// call entirely (no provider request) and emits a tool_result_pruning
// compaction event.
func TestAgent_CompactSkipsSummarizeWhenPruningResolves(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("prune-skip-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 10000,
			Thresholds: CompressionThresholds{
				SoftPercent: 50,
				HardPercent: 95,
			},
			// Opt in: pre-pruning is off by default (bugs.md), so the test
			// exercises the enabled path explicitly.
			ToolResultPruning: ToolResultPruningConfig{Enabled: true},
		},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	// One huge tool result: 20000 runes ≈ 6061 tokens > 50% of the 10k
	// window. After pruning to 5161 runes (≈1564 tokens) the estimate falls
	// under the escalation level (hard−5 = 90%), so summarize must be
	// skipped.
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "go"},
	})
	bigToolPair(agent, "c1", 20000)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	if got := len(p.recorded()); got != 0 {
		t.Errorf("summarize LLM call was made %d time(s); pruning should have resolved pressure", got)
	}
	assertPrunedToolResult(t, agent.GetHistory(), "c1", DefaultToolResultPruningConfig)
	assertSinglePruneCompactionEvent(t, obs)
}

// assertSinglePruneCompactionEvent asserts the only compaction strategy
// observed is the tool_result_pruning pass (summarize was skipped).
func assertSinglePruneCompactionEvent(t *testing.T, obs *mockEventObserver) {
	t.Helper()
	if !obs.HasEventType(EventCompact) {
		t.Error("no EventCompact emitted for the pruning pass")
	}
	for _, e := range obs.Events() {
		if e.Type == EventCompact && e.Compaction != nil && e.Compaction.Strategy != "tool_result_pruning" {
			t.Errorf("unexpected compaction strategy %q (summarize should have been skipped)", e.Compaction.Strategy)
		}
	}
}

// TestAgent_CompactSummarizesWhenPruningInsufficient guards the other side of
// the re-measure gate: when pruning cannot bring the estimate under the
// escalation level, Compact still summarizes (on the pruned history).
func TestAgent_CompactSummarizesWhenPruningInsufficient(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("prune-fallthrough-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			// 1000-token window: the escalation level is hard−5 = 90% = 900
			// tokens. The prunable payload (a 20000-rune tool result) shrinks
			// to ≈1564 tokens, but the non-prunable user content (~2971 runes
			// ≈ 900 tokens) keeps the post-prune estimate at the window edge,
			// so the summarize fallback must still run.
			MaxTokens: 1000,
			Thresholds: CompressionThresholds{
				SoftPercent: 50,
				HardPercent: 95,
			},
			ToolResultPruning: ToolResultPruningConfig{Enabled: true},
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: strings.Repeat("u", 2971)},
	})
	bigToolPair(agent, "c1", 20000)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	if got := len(p.recorded()); got != 1 {
		t.Fatalf("summarize LLM call count = %d, want 1 (pruning could not resolve pressure)", got)
	}
	// The summarize request must carry the PRUNED surface (the model-free
	// pass runs ahead of range selection).
	if text := contextText(p.recorded()[0]); !strings.Contains(text, strings.TrimSpace(PruneMarker)) {
		t.Error("summarize request did not carry the pruned tool result")
	}
}

// TestAgent_CompactEmptyConfigUsesPruneDefaults verifies the SDK default
// resolution: a zero ToolResultPruningConfig still prunes with the documented
// 8192/4096/1024 budgets.
func TestAgent_CompactEmptyConfigUsesPruneDefaults(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "go"},
	})
	bigToolPair(agent, "c1", 20000)

	cfg := agent.cfg.ContextCompression.ToolResultPruning.resolve()
	if cfg != DefaultToolResultPruningConfig {
		t.Fatalf("zero config resolved to %+v, want %+v", cfg, DefaultToolResultPruningConfig)
	}

	agent.mu.Lock()
	changed := agent.pruneToolResultsLocked(cfg)
	agent.mu.Unlock()
	if changed != 1 {
		t.Fatalf("default-config pass pruned %d results, want 1", changed)
	}
}

// TestToolResultPruningConfigResolveDegenerate verifies the dsh config rule:
// a head+marker+tail combination that exceeds the threshold cannot prune
// without growth, so resolve falls back to the defaults.
func TestToolResultPruningConfigResolveDegenerate(t *testing.T) {
	bad := ToolResultPruningConfig{ThresholdChars: 100, HeadChars: 90, TailChars: 50}
	if got := bad.resolve(); got != DefaultToolResultPruningConfig {
		t.Errorf("degenerate config resolved to %+v, want defaults %+v", got, DefaultToolResultPruningConfig)
	}

	custom := ToolResultPruningConfig{ThresholdChars: 4000, HeadChars: 2000, TailChars: 500}
	if got := custom.resolve(); got != custom {
		t.Errorf("valid custom config mutated: %+v → %+v", custom, got)
	}
}
