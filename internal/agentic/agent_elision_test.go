// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestBuildProviderHistoryElidedCallsBecomeNotes(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	// Serialization must be non-destructive: the in-history marker remains.
	if !historyHasElisionMarker(agent.GetHistory()) {
		t.Fatal("in-history elision marker missing: serialization must not mutate history")
	}

	msgs := agent.buildProviderHistory()
	assertToolPairingConsistent(t, msgs)

	text := payloadTexts(msgs)
	for _, want := range []string{"[earlier call to bash elided]", "[earlier call to edit elided]"} {
		if !strings.Contains(text, want) {
			t.Errorf("provider-bound history missing note %q; payload:\n%s", want, text)
		}
	}
	// The straddle case: call_2's result was NOT elided in history, but its
	// call was — the result must be dropped, not shipped as an orphan.
	assertNoToolResultsFor(t, msgs, "call_1", "call_2")
}

// TestMigrateMessagesElidedArgumentsValidJSON guards the summarize bug at the
// migrate layer: no assistant tool_call leaving migrateMessages may carry
// non-JSON arguments.
func TestMigrateMessagesElidedArgumentsValidJSON(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	assertToolPairingConsistent(t, migrateMessages(agent.GetHistory()))
}

// TestMigrateMessagesDropsElidedPairs covers parallel tool calls: one
// assistant message with two elided calls yields one plural note and both
// matching results are dropped, while a live pair survives untouched.
func TestMigrateMessagesDropsElidedPairs(t *testing.T) {
	msgs := []Message{
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "e1", Type: "function", Name: "bash", Arguments: elidedToolCallArguments},
				{ID: "e2", Type: "function", Name: "edit", Arguments: elidedToolCallArguments},
			},
		},
		{Type: Content, Role: ToolRole, Content: "[tool result elided]", ToolCallID: "e1"},
		{Type: Content, Role: ToolRole, Content: "[tool result elided]", ToolCallID: "e2"},
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "live", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: "a.txt", ToolCallID: "live"},
	}

	out := migrateMessages(msgs)
	assertToolPairingConsistent(t, out)

	text := payloadTexts(out)
	if !strings.Contains(text, "[earlier calls to bash, edit elided]") {
		t.Errorf("missing plural elision note; payload:\n%s", text)
	}
	liveFound, liveResultFound := false, false
	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockToolCall && b.ToolCallID == "live" {
				liveFound = true
			}
			if b.Type == provider.ContentBlockToolResult && b.ToolCallID == "live" {
				liveResultFound = true
			}
		}
	}
	if !liveFound || !liveResultFound {
		t.Errorf("live tool pair damaged: call=%v result=%v", liveFound, liveResultFound)
	}
}

// TestMigrateMessagesDropsResultsForMissingAssistant is the defense-in-depth
// regression for the export-20260805-180955 bug: when the owning
// assistant(tool_calls) message is removed entirely (not just elided) by
// enforceContextCeiling or any other history mutation, migrateMessages must
// drop the orphaned tool result — a tool message with no preceding tool_calls
// is rejected by strict providers (HTTP 400).
func TestMigrateMessagesDropsResultsForMissingAssistant(t *testing.T) {
	// The assistant that issued "orphan_call" is absent from this snapshot;
	// only its tool result survives. migrateMessages must drop it.
	msgs := []Message{
		{Type: Content, Role: System, Content: "sys"},
		// missing: Assistant with ToolCalls{ID:"orphan_call"}
		{Type: Content, Role: ToolRole, Content: "result", ToolCallID: "orphan_call", ToolName: "read"},
		{Type: Content, Role: Assistant, Content: "ok"},
	}

	out := migrateMessages(msgs)
	assertToolPairingConsistent(t, out)

	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockToolResult && b.ToolCallID == "orphan_call" {
				t.Errorf("orphaned tool result for missing assistant leaked into payload")
			}
		}
	}
}

// TestSummarizeHistoryWithElidedPairs is the /compress:summarize regression:
// a snapshot containing elided pairs must reach the provider with no
// "[elided]" arguments and consistent pairing (the failing request shape
// carried no Tools array, exactly like summarizeHistory).
func TestSummarizeHistoryWithElidedPairs(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := newElisionAgent()
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory failed: %v", err)
	}
	if summary == "" {
		t.Error("summarizeHistory returned an empty summary")
	}

	ctxs := p.recorded()
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 summarize request, got %d", len(ctxs))
	}
	assertToolPairingConsistent(t, ctxs[0].Messages)
	if !strings.Contains(payloadTexts(ctxs[0].Messages), "[earlier call to bash elided]") {
		t.Error("summarize request missing the elision note for elided calls")
	}
}

// --- Provider prefix-cache bust loop:(CM:13) regression tests ---

// purposeProbeProvider records every StreamOptions it receives so tests can
// assert what a summarize request actually carried (P13 purpose attribution).
type purposeProbeProvider struct {
	api provider.Api

	mu   sync.Mutex
	opts []provider.StreamOptions
}

func (p *purposeProbeProvider) API() provider.Api { return p.api }

func (p *purposeProbeProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.opts = append(p.opts, opts)
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "compacted summary"})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "compacted summary"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *purposeProbeProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *purposeProbeProvider) recordedOpts() []provider.StreamOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.StreamOptions(nil), p.opts...)
}

// TestSummarizeHistorySetsCompactionPurpose is the P13 acceptance at the
// agent layer: the compaction summarize call marks its request purpose as
// compaction so DeepSeek-compat routes emit x-goa-compact: 1.
func TestSummarizeHistorySetsCompactionPurpose(t *testing.T) {
	p := &purposeProbeProvider{api: provider.Api(fmt.Sprintf("summarize-purpose-probe-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := newElisionAgent()
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory(elidedPairHistory())

	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory failed: %v", err)
	}
	if summary == "" {
		t.Error("summarizeHistory returned an empty summary")
	}

	opts := p.recordedOpts()
	if len(opts) != 1 {
		t.Fatalf("expected 1 summarize request, got %d", len(opts))
	}
	if opts[0].Purpose != provider.PurposeCompaction {
		t.Errorf("summarize request purpose = %q, want %q", opts[0].Purpose, provider.PurposeCompaction)
	}
}

// historyHash fingerprints message count, contents and tool-call arguments so
// tests can detect any history mutation (in-place elision or message drops) —
// every mutation is a provider prefix-cache bust on the next request.
func historyHash(a *Agent) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := fnv.New64a()
	fmt.Fprintf(h, "%d\x00", len(a.history))
	for i := range a.history {
		m := &a.history[i]
		fmt.Fprintf(h, "%v\x00%s\x00", m.Role, m.Content)
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(h, "%s\x00", tc.Arguments)
		}
	}
	return h.Sum64()
}

// --- Cache-warm compaction summarization (CA1) regression tests ---

// prefixStubTool is a minimal tool registered only so the summarize request
// must carry a tools array identical to the conversation request's.
type prefixStubTool struct{ BaseTool }

func (prefixStubTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "prefix_stub",
		Description: "stub tool for prefix-parity tests",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
	}
}
func (prefixStubTool) Execute(input string) (string, error) { return "ok", nil }
func (prefixStubTool) IsRetryable(err error) bool           { return false }

// TestSummarizeHistoryReusesConversationPrefix is the CA1 regression: the
// summarize request must reuse the warm provider prefix cache, so it must be
// built as the conversation's OWN request prefix — same system prompt, same
// tools, same migrated history — with the compaction instruction appended as
// the final user message. The pre-fix shape swapped in a summarizer system
// prompt and dropped tools, cold-missing the automatic prefix cache (DeepSeek
// context caching) on the largest history of the session.
func TestSummarizeHistoryReusesConversationPrefix(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-prefix-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Tools:        []Tool{prefixStubTool{}},
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			PreserveRecentTurns: 1,
		},
	})
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
		{Type: Content, Role: Assistant, Content: "second answer"},
	})

	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory failed: %v", err)
	}
	if summary == "" {
		t.Fatal("summarizeHistory returned an empty summary")
	}

	ctxs := p.recorded()
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 summarize request, got %d", len(ctxs))
	}
	got := ctxs[0]

	// 1. The conversation's own system prompt is the request prefix — not a
	// swapped-in summarizer system prompt.
	if got.SystemPrompt != agent.cfg.SystemPrompt {
		t.Errorf("summarize request system prompt = %q, want the conversation system prompt %q (prefix-cache reuse)",
			got.SystemPrompt, agent.cfg.SystemPrompt)
	}

	// 2. Tool schemas ride the request exactly as they do on conversation
	// turns, keeping the cached prefix (system + tools + history) aligned.
	conversation := agent.buildProviderContext(context.Background())
	if len(got.Tools) != len(conversation.Tools) {
		t.Errorf("summarize request carries %d tool schemas, conversation request carries %d",
			len(got.Tools), len(conversation.Tools))
	}

	// 3. The message list is the conversation history (leading system prompt
	// skipped, since it rides SystemPrompt) plus ONE appended user message
	// carrying the summarize instruction.
	if len(got.Messages) != len(conversation.Messages)+1 {
		t.Fatalf("summarize request holds %d messages, want conversation history (%d) + 1 instruction",
			len(got.Messages), len(conversation.Messages))
	}
	for i, m := range conversation.Messages {
		if got.Messages[i].Role != m.Role || payloadTexts([]provider.Message{got.Messages[i]}) != payloadTexts([]provider.Message{m}) {
			t.Errorf("message %d diverges from conversation prefix: got role=%v text=%q, want role=%v text=%q",
				i, got.Messages[i].Role, payloadTexts([]provider.Message{got.Messages[i]}), m.Role, payloadTexts([]provider.Message{m}))
		}
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != provider.RoleUser {
		t.Errorf("instruction message role = %v, want user", last.Role)
	}
	if !strings.Contains(strings.ToLower(payloadTexts([]provider.Message{last})), "summar") {
		t.Error("final message does not carry the summarize instruction")
	}
}

// TestSummarizeHistoryEmptyOnToolOnlyReply guards the tools-bearing summarize
// path: a model that answers the instruction with only a tool call (no text)
// must yield an error instead of wiping the history with an empty summary.
func TestSummarizeHistoryEmptyOnToolOnlyReply(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-toolonly-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 99, // always answer with a tool call, never text
	}
	provider.RegisterApiProvider(p)

	agent := newElisionAgent()
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "q"},
		{Type: Content, Role: Assistant, Content: "a"},
	})

	summary, _, err := agent.summarizeHistory(context.Background())
	if err == nil {
		t.Errorf("summarizeHistory returned %q with nil error for a text-less reply; want an error so Compact cannot wipe history", summary)
	}
}
