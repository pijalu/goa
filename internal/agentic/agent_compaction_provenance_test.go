// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// compactionTxEvents returns the provenance-triple events (start / summary /
// end) from the observer, in emission order.
func compactionTxEvents(obs *mockEventObserver) []OutputEvent {
	var out []OutputEvent
	for _, e := range obs.Events() {
		switch e.Type {
		case EventCompactionStart, EventCompactionSummary, EventCompactionEnd:
			out = append(out, e)
		}
	}
	return out
}

// provenanceSummarizeProvider answers the summarize call with a fixed reply
// and attaches provider-reported Usage to the stream result, so tests can
// assert compaction_summary carries the model-call facts (CX4).
type provenanceSummarizeProvider struct {
	api     provider.Api
	reply   string
	usage   *provider.Usage
	failErr error // when set, the stream closes with this error (failed attempt)

	mu    sync.Mutex
	calls int
}

func (p *provenanceSummarizeProvider) API() provider.Api { return p.api }

func (p *provenanceSummarizeProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if p.failErr != nil {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "partial"})
			result.CloseWithError(p.failErr)
			return
		}
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: p.reply})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.reply}},
			StopReason: provider.StopReasonEndTurn,
			Usage:      p.usage,
		})
	}()
	return result, nil
}

func (p *provenanceSummarizeProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *provenanceSummarizeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newProvenanceAgent(p *provenanceSummarizeProvider, history []Message) *Agent {
	provider.RegisterApiProvider(p)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.history = history
	return agent
}

// provenanceHistory builds a history heavy enough that compaction frees a
// positive, measurable amount of tokens (the landed checkpoint is ~preamble
// + summary sized, so a 5-line chat would show a negative freed estimate).
func provenanceHistory() []Message {
	h := []Message{{Type: Content, Role: System, Content: "You are helpful"}}
	for i := 0; i < 4; i++ {
		h = append(h,
			Message{Type: Content, Role: User, Content: strings.Repeat("q", 400)},
			Message{Type: Content, Role: Assistant, Content: strings.Repeat("a", 400)},
		)
	}
	return h
}

// TestAgent_Compact_EmitsProvenanceTriple is the core CX4 acceptance test:
// every compaction emits compaction_start → compaction_summary →
// compaction_end in order, all three sharing one transaction id, and the
// summary event carries the shadowed message range, the freed tokens, the
// provider/model identity and the provider-reported summarize usage (dsh
// compaction types.ts start/summary/end).
func TestAgent_Compact_EmitsProvenanceTriple(t *testing.T) {
	usage := &provider.Usage{InputTokens: 1234, OutputTokens: 56, CacheReadTokens: 1000}
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("provenance-probe-%d", testProviderCounter.Add(1))),
		reply: "## Primary Request and Intent\n- provenance checkpoint",
		usage: usage,
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	before := agent.ContextStats()
	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after := agent.ContextStats()

	txs := compactionTxEvents(obs)
	if len(txs) != 3 {
		t.Fatalf("expected the provenance triple (start, summary, end), got %d events: %v", len(txs), obs.Events())
	}

	assertProvenanceTripleOrder(t, txs)
	assertProvenanceSharedID(t, txs)
	assertProvenanceSummaryFacts(t, txs[1].CompactionTx, before, after, usage)
	assertProvenanceCleanEnd(t, txs[2].CompactionTx)
	assertProvenanceLegacyCompactStillFires(t, obs)
}

// assertProvenanceTripleOrder checks the event types come out in
// start → summary → end order (dsh compaction start/summary/end).
func assertProvenanceTripleOrder(t *testing.T, txs []OutputEvent) {
	t.Helper()
	wantTypes := []EventType{EventCompactionStart, EventCompactionSummary, EventCompactionEnd}
	for i, want := range wantTypes {
		if txs[i].Type != want {
			t.Errorf("event %d type = %q, want %q (triple out of order)", i, txs[i].Type, want)
		}
	}
}

// assertProvenanceSharedID checks the triple shares one non-empty cx-prefixed
// transaction id and that compaction_start carries only the id (no summary
// facts yet).
func assertProvenanceSharedID(t *testing.T, txs []OutputEvent) {
	t.Helper()
	id := txs[0].CompactionTx.ID
	if id == "" || !strings.HasPrefix(id, "cx-") {
		t.Errorf("compaction_start id = %q, want a non-empty cx- prefixed id", id)
	}
	for i, ev := range txs {
		if ev.CompactionTx == nil {
			t.Fatalf("event %d (%s) carries no CompactionTx payload", i, ev.Type)
		}
		if ev.CompactionTx.ID != id {
			t.Errorf("event %d (%s) id = %q, want shared id %q", i, ev.Type, ev.CompactionTx.ID, id)
		}
	}
	start := txs[0].CompactionTx
	if start.ShadowedCount != 0 || start.Provider != "" || start.Error != "" {
		t.Errorf("compaction_start must carry only the id, got %+v", start)
	}
}

// assertProvenanceSummaryFacts checks the compaction_summary payload: the
// shadowed range (the whole pre-compaction history — 1 system + 4×2 turns =
// 9 messages), provider/model identity, the provider-reported summarize
// usage, and the freed estimate measured against the landed checkpoint.
func assertProvenanceSummaryFacts(t *testing.T, sum *CompactionTx, before, after ContextStats, usage *provider.Usage) {
	t.Helper()
	if sum.ShadowedStart != 0 || sum.ShadowedEnd != 9 || sum.ShadowedCount != 9 {
		t.Errorf("shadowed range = [%d,%d) count=%d, want [0,9) count=9",
			sum.ShadowedStart, sum.ShadowedEnd, sum.ShadowedCount)
	}
	if sum.Provider != string(provider.ProviderCustom) {
		t.Errorf("Provider = %q, want %q", sum.Provider, provider.ProviderCustom)
	}
	if sum.Model != "test-model" {
		t.Errorf("Model = %q, want test-model", sum.Model)
	}
	assertProvenanceUsage(t, sum, usage)
	if wantFreed := before.EstimatedTokens - after.EstimatedTokens; sum.FreedTokens != wantFreed {
		t.Errorf("FreedTokens = %d, want the measured before−after delta %d", sum.FreedTokens, wantFreed)
	}
	if sum.FreedTokens <= 0 {
		t.Errorf("FreedTokens = %d, want > 0 (the checkpoint is smaller than the shadowed history)", sum.FreedTokens)
	}
	if sum.ShadowedTokens != before.EstimatedTokens {
		t.Errorf("ShadowedTokens = %d, want the pre-compaction estimate %d (shadow price of the replaced range)",
			sum.ShadowedTokens, before.EstimatedTokens)
	}
}

// assertProvenanceUsage checks the provider-reported summarize usage survived
// on the compaction_summary event.
func assertProvenanceUsage(t *testing.T, sum *CompactionTx, usage *provider.Usage) {
	t.Helper()
	if sum.Usage == nil || sum.Usage.InputTokens != usage.InputTokens ||
		sum.Usage.OutputTokens != usage.OutputTokens || sum.Usage.CacheReadTokens != usage.CacheReadTokens {
		t.Errorf("Usage = %+v, want the provider-reported summarize usage %+v", sum.Usage, usage)
	}
}

// assertProvenanceCleanEnd checks a successful compaction closes the
// transaction with an empty error.
func assertProvenanceCleanEnd(t *testing.T, end *CompactionTx) {
	t.Helper()
	if end.Error != "" {
		t.Errorf("compaction_end Error = %q, want empty on success", end.Error)
	}
}

// assertProvenanceLegacyCompactStillFires checks the surviving EventCompact
// still fires once (public API contract).
func assertProvenanceLegacyCompactStillFires(t *testing.T, obs *mockEventObserver) {
	t.Helper()
	if n := len(compactEvents(obs)); n != 1 {
		t.Errorf("expected the existing EventCompact to still fire once, got %d", n)
	}
}

// TestAgent_Compact_ProvenanceOnFailure verifies the failed-attempt shape
// (dsh compaction/end {error}): start → end with the error recorded, NO
// summary event, history untouched. A failed compaction is a closed
// transaction, not an orphan.
func TestAgent_Compact_ProvenanceOnFailure(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:     provider.Api(fmt.Sprintf("provenance-fail-probe-%d", testProviderCounter.Add(1))),
		failErr: fmt.Errorf("boom: stream died"),
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	err := agent.Compact(context.Background())
	if err == nil {
		t.Fatal("Compact must return the summarize error")
	}

	txs := compactionTxEvents(obs)
	if len(txs) != 2 {
		t.Fatalf("failed compaction must emit start + end only, got %d events: %v", len(txs), obs.Events())
	}
	if txs[0].Type != EventCompactionStart || txs[1].Type != EventCompactionEnd {
		t.Fatalf("failed compaction order = [%s %s], want [compaction_start compaction_end]", txs[0].Type, txs[1].Type)
	}
	if txs[0].CompactionTx.ID != txs[1].CompactionTx.ID {
		t.Errorf("start id %q != end id %q", txs[0].CompactionTx.ID, txs[1].CompactionTx.ID)
	}
	if !strings.Contains(txs[1].CompactionTx.Error, "boom") {
		t.Errorf("compaction_end must record the failure, Error = %q", txs[1].CompactionTx.Error)
	}

	// A failed compaction is NOT an orphan: the transaction closed.
	if orphans := FindOrphanedCompactions(obs.Events()); len(orphans) != 0 {
		t.Errorf("failed compaction reported as orphan %v; end-with-error must close the transaction", orphans)
	}

	// History is untouched (no checkpoint landed).
	agent.mu.Lock()
	n := len(agent.history)
	agent.mu.Unlock()
	if n != 9 {
		t.Errorf("history = %d messages after failed Compact, want the original 9", n)
	}
}

// TestFindOrphanedCompactions_KillBetweenStartAndEnd is the CX4 crash test:
// a process killed between compaction_start and compaction_end leaves a
// start (and possibly a summary) with no end. The next boot scans the log
// and must report exactly those ids — completed and failed transactions are
// not orphans.
func TestFindOrphanedCompactions_KillBetweenStartAndEnd(t *testing.T) {
	start := func(id string) OutputEvent {
		return OutputEvent{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: id}}
	}
	summary := func(id string) OutputEvent {
		return OutputEvent{Type: EventCompactionSummary, CompactionTx: &CompactionTx{ID: id, ShadowedCount: 5}}
	}
	end := func(id, errMsg string) OutputEvent {
		return OutputEvent{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: id, Error: errMsg}}
	}

	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "earlier turn"}, // noise: ignored
		start("cx-ok-1"), summary("cx-ok-1"), end("cx-ok-1", ""), // completed
		start("cx-failed-2"), end("cx-failed-2", "summarize blew up"), // failed but closed
		start("cx-crashed-3"), summary("cx-crashed-3"), // KILLED before end
		start("cx-crashed-4"),                   // KILLED right after start
		{Type: EventCompact, Text: "summarize"}, // legacy event: no tx payload, ignored
	}

	orphans := FindOrphanedCompactions(events)
	if len(orphans) != 2 {
		t.Fatalf("orphans = %v, want exactly the two killed transactions", orphans)
	}
	if orphans[0] != "cx-crashed-3" || orphans[1] != "cx-crashed-4" {
		t.Errorf("orphans = %v, want [cx-crashed-3 cx-crashed-4] in first-start order", orphans)
	}
}

// TestFindOrphanedCompactions_Empty covers the no-compaction log: nothing to
// report, nil result (not an empty non-nil slice) so callers can len()==0.
func TestFindOrphanedCompactions_Empty(t *testing.T) {
	if orphans := FindOrphanedCompactions(nil); len(orphans) != 0 {
		t.Errorf("orphans = %v on an empty log, want none", orphans)
	}
}

// TestAgent_Compact_PruneResolvedSkipsTransaction pins the boundary of the
// CX4 contract: when the pre-compaction tool-result pruner (CX1) resolves
// context pressure on its own, the summarize never runs — so no compaction
// transaction opens (there is no summary to record) and the only signal is
// the existing tool_result_pruning EventCompact.
func TestAgent_Compact_PruneResolvedSkipsTransaction(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("provenance-prune-probe-%d", testProviderCounter.Add(1))),
		reply: "should never be requested",
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
		},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	// One huge tool result: 20000 runes ≈ 6061 tokens > 50% of the 10k
	// window. After pruning the estimate falls under the escalation level, so
	// summarize must be skipped (proven CX1 shape).
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "go"},
	})
	bigToolPair(agent, "c1", 20000)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if p.callCount() != 0 {
		t.Errorf("summarize called %d times though pruning resolved the pressure", p.callCount())
	}
	if txs := compactionTxEvents(obs); len(txs) != 0 {
		t.Errorf("prune-resolved pass must not open a compaction transaction, got %v", txs)
	}
}
