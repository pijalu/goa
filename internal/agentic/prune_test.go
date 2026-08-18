// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// --- Stale tool-output pruning (P4) tests ---

// staleToolBody returns an ASCII tool-result body of roughly wantTokens
// estimated tokens (estimateTokens: ascii chars ×10/33).
func staleToolBody(wantTokens int) string {
	return strings.Repeat("y", wantTokens*33/10)
}

// staleHistory builds a history of numBodies old tool pairs (assistant call +
// tool result of ~1200 tokens each) followed by recentMsgs small user
// messages. Tool pair i (0-based) lives at indexes 2i+1 (call) and 2i+2
// (result); the tool result of pair i is at history index 2i+2.
func staleHistory(numBodies, recentMsgs int) []Message {
	h := []Message{{Type: Content, Role: User, Content: "start"}}
	for i := 0; i < numBodies; i++ {
		id := fmt.Sprintf("c%d", i)
		h = append(h,
			Message{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{{ID: id, Type: "function", Name: "bash", Arguments: `{"cmd":"dump"}`}}},
			Message{Type: Content, Role: ToolRole, Content: staleToolBody(1200), ToolCallID: id, ToolName: "bash"},
		)
	}
	for i := 0; i < recentMsgs; i++ {
		h = append(h, Message{Type: Content, Role: User, Content: fmt.Sprintf("recent %d", i)})
	}
	return h
}

// toolResultIndex returns the history index of the ToolRole message for tool
// pair i in the staleHistory layout.
func toolResultIndex(i int) int {
	return 2*i + 2
}

// TestPruneStaleToolOutput_Gate verifies the reclaimable gate: a short session
// whose reclaimable total is below the threshold is untouched.
func TestPruneStaleToolOutput_Gate(t *testing.T) {
	// 10 bodies × ~1200 tokens ≈ 12K tokens reclaimable < 20K gate.
	h := staleHistory(10, 3)
	changed, reclaimed := pruneStaleToolOutput(h)
	if changed != 0 {
		t.Errorf("short session should not prune: changed=%d", changed)
	}
	if reclaimed != 0 {
		t.Errorf("short session should report no reclaim: reclaimed=%d", reclaimed)
	}
	for i := 0; i < 10; i++ {
		if got := h[toolResultIndex(i)].Content; strings.HasPrefix(got, stalePrunePrefix) {
			t.Errorf("body %d pruned despite gate: %q", i, got)
		}
	}
}

// TestPruneStaleToolOutput_ProtectsRecentWindow verifies the window semantics:
// the oldest large bodies are replaced with placeholders while the recent
// window (the last messages plus the trailing token budget) stays byte-identical.
func TestPruneStaleToolOutput_ProtectsRecentWindow(t *testing.T) {
	const bodies, recent = 60, 10
	h := staleHistory(bodies, recent)
	changed, reclaimed := pruneStaleToolOutput(h)
	if changed == 0 {
		t.Fatal("long dump-heavy session should prune stale bodies")
	}
	if reclaimed < stalePruneGateTokens {
		t.Errorf("reclaimed %d < gate %d", reclaimed, stalePruneGateTokens)
	}
	assertPrunedBodies(t, h, bodies, recent)
	assertRecentMessages(t, h, bodies, recent)
	if h[0].Content != "start" {
		t.Error("leading user message changed")
	}
	idx := toolResultIndex(0)
	if h[idx].ToolCallID != "c0" || h[idx].ToolName != "bash" {
		t.Errorf("pruned body lost pairing: id=%q name=%q", h[idx].ToolCallID, h[idx].ToolName)
	}
}

func assertPrunedBodies(t *testing.T, h []Message, bodies, recent int) {
	pruned := 0
	for i := 0; i < bodies; i++ {
		if strings.HasPrefix(h[toolResultIndex(i)].Content, stalePrunePrefix) {
			pruned++
		}
	}
	if pruned == 0 {
		t.Error("no body was pruned")
	}
	if pruned > bodies-recent {
		t.Errorf("pruned %d bodies, expected recent window to protect %d", pruned, recent)
	}
	protected := 0
	for i := bodies - 1; i >= 0; i-- {
		if strings.HasPrefix(h[toolResultIndex(i)].Content, stalePrunePrefix) {
			break
		}
		protected++
	}
	if protected < 25 {
		t.Errorf("recent window should protect newest bodies, protected=%d", protected)
	}
}

func assertRecentMessages(t *testing.T, h []Message, bodies, recent int) {
	for i := 0; i < recent; i++ {
		if got, want := h[bodies*2+1+i].Content, fmt.Sprintf("recent %d", i); got != want {
			t.Errorf("recent message %d changed to %q", i, got)
		}
	}
}

// TestPruneStaleToolOutput_Placeholder verifies the placeholder carries the
// tool name and the estimated token count of the original body.
func TestPruneStaleToolOutput_Placeholder(t *testing.T) {
	const bodies, recent = 60, 10
	h := staleHistory(bodies, recent)
	changed, _ := pruneStaleToolOutput(h)
	if changed == 0 {
		t.Fatal("expected stale bodies to prune")
	}
	idx := toolResultIndex(0) // oldest body
	content := h[idx].Content
	if !strings.Contains(content, "bash") {
		t.Errorf("placeholder should name the tool, got %q", content)
	}
	if !strings.Contains(content, "~1200 tokens") {
		t.Errorf("placeholder should carry the original token estimate, got %q", content)
	}
	if !strings.Contains(content, "re-run the tool if needed") {
		t.Errorf("placeholder should tell the model to re-run, got %q", content)
	}
}

// TestPruneStaleToolOutput_NonToolMessagesUntouched verifies non-tool
// messages — even large ones in the old region — are never pruned.
func TestPruneStaleToolOutput_NonToolMessagesUntouched(t *testing.T) {
	const bodies, recent = 60, 10
	h := staleHistory(bodies, recent)
	// Inject a large user message in the OLD region (before the tool pairs).
	h = append([]Message{{Type: Content, Role: User, Content: staleToolBody(3000)}}, h...)
	// The body index shift: tool pair i now lives at 2i+2+1.
	changed, _ := pruneStaleToolOutput(h)
	if changed == 0 {
		t.Fatal("expected stale bodies to prune")
	}
	if got := h[0].Content; got != staleToolBody(3000) {
		t.Error("large old user message was pruned")
	}
}

// TestPruneStaleToolOutput_Idempotent verifies a second pass replaces nothing.
func TestPruneStaleToolOutput_Idempotent(t *testing.T) {
	const bodies, recent = 60, 10
	h := staleHistory(bodies, recent)
	first, _ := pruneStaleToolOutput(h)
	if first == 0 {
		t.Fatal("first pass should prune")
	}
	second, secondReclaimed := pruneStaleToolOutput(h)
	if second != 0 {
		t.Errorf("second pass pruned %d bodies — not idempotent", second)
	}
	if secondReclaimed != 0 {
		t.Errorf("second pass reported %d reclaimed — not idempotent", secondReclaimed)
	}
}

// TestAgent_CompactStalePruningSkipsSummarize is the P4 wiring acceptance:
// a dump-heavy session triggers the stale pass inside the pre-compaction path;
// when pruning resolves the pressure, Compact skips the summarize LLM call and
// emits a tool_result_pruning compaction event (goa's existing early-return).
func TestAgent_CompactStalePruningSkipsSummarize(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("stale-prune-skip-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 200000,
			Thresholds: CompressionThresholds{
				SoftPercent: 50,
				HardPercent: 95,
			},
		},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	agent.SetHistory(staleHistory(90, 10))

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	if got := len(p.recorded()); got != 0 {
		t.Errorf("summarize LLM call was made %d time(s); stale pruning should have resolved pressure", got)
	}
	assertSinglePruneCompactionEvent(t, obs)
	// The stale placeholder must be present in the landed history.
	found := 0
	for _, m := range agent.GetHistory() {
		if strings.HasPrefix(m.Content, stalePrunePrefix) {
			found++
		}
	}
	if found == 0 {
		t.Error("stale pass did not run inside Compact (no pruned bodies in history)")
	}
}
