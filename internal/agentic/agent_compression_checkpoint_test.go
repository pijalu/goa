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

// compactionCheckpointHeaders are the 8 sections of the CX3 checkpoint
// contract, in the exact order the instruction must list them (ported
// verbatim from dsh compaction-basic summarizer.ts COMPACTION_INSTRUCTION).
var compactionCheckpointHeaders = []string{
	"## Primary Request and Intent",
	"## Key Technical Concepts",
	"## Files and Code",
	"## Errors and Fixes",
	"## Pending Jobs",
	"## Current Work",
	"## Next Step",
	"## Critical Context",
}

// TestCompactSummaryInstruction_HasAllEightHeaders is the CX3 snapshot test:
// the summarize instruction carries the full 8-section Markdown contract with
// every header present, in order, and references the <compacted-summary> tag
// so later compactions consolidate a prior checkpoint instead of duplicating
// it. It also pins the SPDX HTML comment strip: the embedded file's license
// header must never consume LLM context.
func TestCompactSummaryInstruction_HasAllEightHeaders(t *testing.T) {
	instruction := compactSummaryInstruction
	if instruction == "" {
		t.Fatal("compactSummaryInstruction is empty; //go:embed of compaction_instruction.md failed")
	}
	if strings.Contains(instruction, "SPDX") || strings.Contains(instruction, "<!--") {
		t.Error("instruction still carries the SPDX HTML comment; embeddoc.StripHTMLComments must run at init")
	}

	prev := -1
	for _, header := range compactionCheckpointHeaders {
		idx := strings.Index(instruction, header)
		if idx < 0 {
			t.Errorf("instruction missing checkpoint header %q", header)
			continue
		}
		if idx < prev {
			t.Errorf("checkpoint header %q out of order (at byte %d after %d)", header, idx, prev)
		}
		prev = idx
	}

	if !strings.Contains(instruction, compactSummaryOpenTag) {
		t.Errorf("instruction must name %q so later compactions merge the prior checkpoint", compactSummaryOpenTag)
	}
}

// TestFrameCompactedSummary pins the durable checkpoint framing (dsh
// compaction-basic frameSummary): preamble + <compacted-summary> + summary +
// </compacted-summary>.
func TestFrameCompactedSummary(t *testing.T) {
	framed := frameCompactedSummary("S1 body")
	if !strings.HasPrefix(framed, compactSummaryPreamble) {
		t.Errorf("framed checkpoint must open with the preamble, got %q", framed[:min(60, len(framed))])
	}
	open := strings.Index(framed, compactSummaryOpenTag)
	body := strings.Index(framed, "S1 body")
	close := strings.Index(framed, compactSummaryCloseTag)
	if open < 0 || body < 0 || close < 0 || !(open < body && body < close) {
		t.Errorf("framed checkpoint must be preamble + open tag + summary + close tag, got %q", framed)
	}
}

// TestAgent_CompactEmitsStructuredCheckpoint pins the landed history shape
// after Compact (CX3): the checkpoint rides a [user, assistant] pair so the
// role sequence stays valid for strict providers (the DeepSeek
// assistant-first regression), and the assistant message carries the summary
// wrapped in <compacted-summary> tags.
func TestAgent_CompactEmitsStructuredCheckpoint(t *testing.T) {
	p := textEventProvider("## Primary Request and Intent\n- test checkpoint body")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.history = []Message{
		{Type: Content, Role: System, Content: "You are helpful"},
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
	}

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	history := make([]Message, len(agent.history))
	copy(history, agent.history)
	agent.mu.Unlock()

	if len(history) != 2 {
		t.Fatalf("expected the [checkpoint-request, checkpoint] pair, got %d messages", len(history))
	}
	if history[0].Role != User {
		t.Errorf("first message role = %v, want user (no assistant-first history)", history[0].Role)
	}
	if history[1].Role != Assistant {
		t.Errorf("second message role = %v, want assistant", history[1].Role)
	}
	if !strings.HasPrefix(history[0].Content, compactSummaryPreamble) {
		t.Errorf("checkpoint request must open with the framing preamble, got %q", history[0].Content[:min(60, len(history[0].Content))])
	}
	if !strings.Contains(history[0].Content, compactSummaryOpenTag) ||
		!strings.Contains(history[0].Content, compactSummaryCloseTag) {
		t.Error("checkpoint request must wrap the summary in <compacted-summary> tags")
	}
	if !strings.Contains(history[0].Content, "test checkpoint body") {
		t.Error("checkpoint request must carry the summary body")
	}
}

// scriptedSummarizeProvider serves a fixed sequence of text replies and
// records every provider context it receives, so tests can assert what each
// compaction's summarize request carried.
type scriptedSummarizeProvider struct {
	api     provider.Api
	replies []string

	mu   sync.Mutex
	ctxs []provider.Context
}

func (p *scriptedSummarizeProvider) API() provider.Api { return p.api }

func (p *scriptedSummarizeProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	call := len(p.ctxs)
	p.ctxs = append(p.ctxs, ctx)
	p.mu.Unlock()

	reply := p.replies[min(call, len(p.replies)-1)]
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: reply})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: reply}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *scriptedSummarizeProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *scriptedSummarizeProvider) recorded() []provider.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Context(nil), p.ctxs...)
}

// TestAgent_RecompactionFeedsPriorCheckpoint is the CX3 chaining test: the
// first compaction lands a <compacted-summary> checkpoint in history, and the
// SECOND compaction's summarize request must replay that prior checkpoint as
// part of its conversation prefix (buildProviderContext replays history
// verbatim), so the model can merge still-true facts instead of losing them.
func TestAgent_RecompactionFeedsPriorCheckpoint(t *testing.T) {
	p := &scriptedSummarizeProvider{
		api:     provider.Api(fmt.Sprintf("recompact-probe-%d", testProviderCounter.Add(1))),
		replies: []string{"CHECKPOINT_ONE body", "CHECKPOINT_TWO body"},
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.history = []Message{
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
	}

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	// New conversation on top of the landed checkpoint, then compact again.
	agent.history = append(agent.history,
		Message{Type: Content, Role: User, Content: "follow-up question"},
		Message{Type: Content, Role: Assistant, Content: "follow-up answer"},
	)
	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("second Compact: %v", err)
	}

	ctxs := p.recorded()
	if len(ctxs) != 2 {
		t.Fatalf("expected 2 summarize requests, got %d", len(ctxs))
	}
	secondRequestText := payloadTexts(ctxs[1].Messages)
	if !strings.Contains(secondRequestText, compactSummaryOpenTag) {
		t.Error("second summarize request must replay the prior <compacted-summary> checkpoint")
	}
	if !strings.Contains(secondRequestText, "CHECKPOINT_ONE body") {
		t.Error("second summarize request must carry the first checkpoint's summary body")
	}

	// The second compaction's own checkpoint lands the same framed shape.
	agent.mu.Lock()
	landed := agent.history
	agent.mu.Unlock()
	if len(landed) != 2 {
		t.Fatalf("after second Compact expected 2 messages, got %d", len(landed))
	}
	if !strings.Contains(landed[0].Content, "CHECKPOINT_TWO body") ||
		!strings.Contains(landed[0].Content, compactSummaryOpenTag) {
		t.Error("second compaction must land its own framed checkpoint")
	}
}
