// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// remoteCompactProbeProvider is a fake provider that implements both the
// streaming ApiProvider contract and the RemoteCompactor capability, so the
// agent can exercise the full remote-compaction path without a live endpoint.
type remoteCompactProbeProvider struct {
	api provider.Api

	// replacement is the transcript the compact call returns.
	replacement []provider.Message
	usage       *provider.Usage
	// compactErr, when set, makes the compact call fail (fallback path).
	compactErr error
	// summarizeReply is the local summarize fallback reply.
	summarizeReply string

	mu           sync.Mutex
	compactCalls int
	streamCalls  int
	// lastCompactReq captures the compact request for assertions.
	lastCompactReq provider.CompactRequest
}

func (p *remoteCompactProbeProvider) API() provider.Api { return p.api }

func (p *remoteCompactProbeProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.streamCalls++
	p.mu.Unlock()
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: p.summarizeReply})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.summarizeReply}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *remoteCompactProbeProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// Compact implements provider.RemoteCompactor.
func (p *remoteCompactProbeProvider) Compact(ctx context.Context, req provider.CompactRequest) (*provider.CompactResponse, error) {
	p.mu.Lock()
	p.compactCalls++
	p.lastCompactReq = req
	p.mu.Unlock()
	if p.compactErr != nil {
		return nil, p.compactErr
	}
	return &provider.CompactResponse{Messages: p.replacement, Usage: p.usage}, nil
}

func (p *remoteCompactProbeProvider) compactCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.compactCalls
}

func (p *remoteCompactProbeProvider) streamCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamCalls
}

// newRemoteCompactAgent builds an agent whose model resolves to the probe
// provider and whose remote-compaction availability is forced on (the 2b.1
// gate is tested separately; here we exercise the 2b.2 execution path).
func newRemoteCompactAgent(p *remoteCompactProbeProvider, history []Message) *Agent {
	provider.RegisterApiProvider(p)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.history = history
	// Force availability on: the embedded/custom profile does not advertise
	// remote compaction, so we stub the memoized gate for the execution test.
	agent.remoteCompactAvailableFn = func() bool { return true }
	return agent
}

// remoteReplacement returns a short canonical replacement transcript.
func remoteReplacement() []provider.Message {
	return []provider.Message{
		provider.NewUserMessage("condensed: earlier conversation"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: provider.ContentBlockText, Text: "acknowledged"}}),
	}
}

// TestCompactRemote_ReplacesHistory verifies the happy path: when availability
// allows, Compact uses the remote endpoint, replaces history with the returned
// transcript, advances the cache generation, and never runs a local summarize.
func TestCompactRemote_ReplacesHistory(t *testing.T) {
	usage := &provider.Usage{InputTokens: 5000, OutputTokens: 120}
	p := &remoteCompactProbeProvider{
		api:            provider.Api(fmt.Sprintf("remote-compact-probe-%d", testProviderCounter.Add(1))),
		replacement:    remoteReplacement(),
		usage:          usage,
		summarizeReply: "local summary should not run",
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	genBefore := agent.cacheGeneration
	keyBefore := agent.cacheKey(agent.cfg.Model)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if p.compactCallCount() != 1 {
		t.Errorf("remote compact call count = %d, want 1", p.compactCallCount())
	}
	if p.streamCallCount() != 0 {
		t.Errorf("local summarize must not run when remote succeeds; stream calls = %d", p.streamCallCount())
	}

	// History replaced by the returned transcript (converted to internal form).
	if len(agent.history) != 2 {
		t.Fatalf("history length = %d, want 2: %#v", len(agent.history), agent.history)
	}
	if agent.history[0].Role != User || !strings.Contains(agent.history[0].Content, "condensed") {
		t.Errorf("history[0] = %#v, want the condensed user message", agent.history[0])
	}
	if agent.history[1].Role != Assistant {
		t.Errorf("history[1].Role = %q, want assistant", agent.history[1].Role)
	}

	// Cache generation advanced and the SSE-path cache key rotated.
	if agent.cacheGeneration != genBefore+1 {
		t.Errorf("cacheGeneration = %d, want %d", agent.cacheGeneration, genBefore+1)
	}
	if keyAfter := agent.cacheKey(agent.cfg.Model); keyAfter == keyBefore {
		t.Error("cache key must rotate after the non-prefix history replacement")
	}

	// Provenance triple shares one id, ordered start → summary → end, with the
	// remote_compact strategy label on the surviving EventCompact.
	txs := compactionTxEvents(obs)
	if len(txs) != 3 {
		t.Fatalf("expected provenance triple, got %d: %v", len(txs), obs.Events())
	}
	assertProvenanceTripleOrder(t, txs)
	assertProvenanceSharedID(t, txs)
	assertProvenanceCleanEnd(t, txs[2].CompactionTx)
	if txs[1].CompactionTx.Usage != usage {
		t.Errorf("compaction_summary usage = %+v, want the remote-call usage %+v", txs[1].CompactionTx.Usage, usage)
	}
	evs := compactEvents(obs)
	if len(evs) != 1 || evs[0].Compaction.Strategy != string(CompressionRemoteCompact) {
		t.Errorf("EventCompact strategy = %+v, want remote_compact", compactEvents(obs))
	}
}

// TestCompactRemote_SendsNormalRequestFields verifies the compact request
// carries the conversation prefix and session/cache identity (never secrets in
// the request beyond the bearer credential).
func TestCompactRemote_SendsNormalRequestFields(t *testing.T) {
	p := &remoteCompactProbeProvider{
		api:         provider.Api(fmt.Sprintf("remote-fields-probe-%d", testProviderCounter.Add(1))),
		replacement: remoteReplacement(),
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	req := p.lastCompactReq
	if req.Options.PromptCacheKey == "" {
		t.Error("compact request must carry the current prompt cache key")
	}
	if req.Options.Purpose != provider.PurposeCompaction {
		t.Errorf("compact request Purpose = %q, want compaction", req.Options.Purpose)
	}
	if req.Context.SystemPrompt == "" {
		t.Error("compact request must carry the system prompt (instructions)")
	}
	if len(req.Context.Messages) == 0 {
		t.Error("compact request must carry the conversation messages")
	}
	if req.Model.ID != agent.cfg.Model.ID {
		t.Errorf("compact request model = %q, want %q", req.Model.ID, agent.cfg.Model.ID)
	}
}

// TestCompactRemote_FailureFallsBack verifies a remote failure leaves history
// untouched and falls back to the local summarize ladder (never a partial
// apply), closing the transaction with the local outcome.
func TestCompactRemote_FailureFallsBack(t *testing.T) {
	p := &remoteCompactProbeProvider{
		api:            provider.Api(fmt.Sprintf("remote-fail-probe-%d", testProviderCounter.Add(1))),
		compactErr:     errors.New("endpoint exploded"),
		summarizeReply: "## Primary Request and Intent\n- local fallback summary",
	}
	history := provenanceHistory()
	agent := newRemoteCompactAgent(p, history)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact must fall back, not fail: %v", err)
	}
	if p.compactCallCount() != 1 {
		t.Errorf("remote compact attempted once, got %d", p.compactCallCount())
	}
	if p.streamCallCount() != 1 {
		t.Errorf("local summarize fallback must run once, got %d", p.streamCallCount())
	}
	// The local fallback landed its [framed-summary, summary] pair.
	if len(agent.history) != 2 || !strings.Contains(agent.history[0].Content, compactSummaryOpenTag) {
		t.Errorf("local fallback history = %#v, want the framed checkpoint pair", agent.history)
	}
	// One clean transaction (remote failure absorbed, local summarize succeeded).
	txs := compactionTxEvents(obs)
	if len(txs) != 3 {
		t.Fatalf("expected one provenance triple, got %d: %v", len(txs), obs.Events())
	}
	assertProvenanceCleanEnd(t, txs[2].CompactionTx)
}

// TestCompactRemote_RetainedBudget verifies the replacement transcript is
// bounded by the retained-message budget: an oversized returned tail is
// trimmed newest-first so the landed history stays under the budget.
func TestCompactRemote_RetainedBudget(t *testing.T) {
	// Each message ~1000 chars ≈ 250 tokens; budget 300 tokens keeps only the
	// newest message.
	big := func(text string) provider.Message {
		return provider.NewUserMessage(strings.Repeat("x", 1000) + text)
	}
	p := &remoteCompactProbeProvider{
		api: provider.Api(fmt.Sprintf("remote-budget-probe-%d", testProviderCounter.Add(1))),
		replacement: []provider.Message{
			big("-old"), big("-mid"), big("-new"),
		},
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.RemoteCompactRetainedBudget = 300
	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(agent.history) != 1 {
		t.Fatalf("retained budget must trim the tail to the newest message, got %d: %#v", len(agent.history), agent.history)
	}
	if !strings.HasSuffix(agent.history[0].Content, "-new") {
		t.Errorf("kept message = %q, want the newest (-new)", agent.history[0].Content)
	}
}

// TestCompactRemote_EmptyReplacementFallsBack verifies a remote call that
// returns no usable messages is treated as a failure (history untouched,
// local fallback runs) rather than wiping the conversation.
func TestCompactRemote_EmptyReplacementFallsBack(t *testing.T) {
	p := &remoteCompactProbeProvider{
		api:            provider.Api(fmt.Sprintf("remote-empty-probe-%d", testProviderCounter.Add(1))),
		replacement:    nil,
		summarizeReply: "## Primary Request and Intent\n- local fallback",
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact must fall back, not fail: %v", err)
	}
	if p.streamCallCount() != 1 {
		t.Errorf("empty replacement must fall back to local summarize, stream calls = %d", p.streamCallCount())
	}
}

// TestCompactRemote_UnsupportedProvider verifies a provider that does not
// implement RemoteCompactor is reported unsupported (applied=false) and the
// local ladder runs.
func TestCompactRemote_UnsupportedProvider(t *testing.T) {
	// provenanceSummarizeProvider implements Stream but NOT Compact.
	sp := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("remote-unsupported-probe-%d", testProviderCounter.Add(1))),
		reply: "## Primary Request and Intent\n- local summary",
	}
	provider.RegisterApiProvider(sp)
	agent := NewAgent(Config{Model: testModel(sp.API()), SystemPrompt: "You are helpful", Logger: NewLogger(Error)})
	agent.history = provenanceHistory()
	agent.remoteCompactAvailableFn = func() bool { return true }

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact must fall back to local summarize: %v", err)
	}
	if sp.callCount() != 1 {
		t.Errorf("unsupported provider must fall back to local summarize once, got %d", sp.callCount())
	}
}

// TestIsRemoteCompactFailure verifies the distinct error wrapper is detectable
// via errors.As for the fallback ordering.
func TestIsRemoteCompactFailure(t *testing.T) {
	if !isRemoteCompactFailure(&errRemoteCompactFailed{cause: errors.New("x")}) {
		t.Error("errRemoteCompactFailed must be detected as a remote failure")
	}
	if isRemoteCompactFailure(errors.New("plain")) {
		t.Error("a plain error must not be classified as a remote failure")
	}
	// Wrapped chains still resolve.
	wrapped := fmt.Errorf("outer: %w", &errRemoteCompactFailed{cause: errors.New("y")})
	if !isRemoteCompactFailure(wrapped) {
		t.Error("wrapped errRemoteCompactFailed must be detected")
	}
}
