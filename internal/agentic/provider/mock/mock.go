// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package mock provides a scripted LLM provider for tests that need
// deterministic, concurrency-safe control over multi-agent turns: per-model
// FIFO reply scripts (text / thinking / tool calls) and optional gate
// channels so tests can hold one role's stream open while asserting another
// role's UI output (team UI bugs RC-1..RC-3 filmstrip scenarios).
package mock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

var instanceCounter atomic.Int64

// Turn is one scripted LLM reply: the streamed events plus the final
// accumulated assistant message. Hold, when non-nil, blocks the stream
// BEFORE the first text event (or before End when the turn has no text
// events) until the channel is closed — thinking and tool-call events
// stream through immediately so tests can observe the working/thinking
// state, then release the reply deterministically.
type Turn struct {
	Events []schema.AssistantMessageEvent
	Final  *schema.AssistantMessage
	Hold   chan struct{}
}

// Provider implements provider.ApiProvider with per-model scripted turns.
// Each Stream call for a model consumes that model's next scripted turn
// (FIFO); when the queue is empty the provider replays the LAST scripted
// turn (agents loop on tool calls — an exhausted queue must not deadlock
// the turn), or a canned default reply when nothing was scripted at all.
type Provider struct {
	api provider.Api

	mu      sync.Mutex
	turns   map[string][]Turn  // model ID → scripted turns
	errs    map[string][]error // model ID → scripted Stream errors (FIFO)
	gates   map[string]chan struct{}
	calls   map[string]int
	lastIdx map[string]int
}

// New registers a fresh mock provider under a unique API type (safe for
// parallel tests) and returns it. t is used only for cleanup-friendly
// construction; pass nil when no testing.TB is available.
func New(t testing.TB) *Provider {
	p := &Provider{
		api:     provider.Api(fmt.Sprintf("test-mock-%d", instanceCounter.Add(1))),
		turns:   map[string][]Turn{},
		errs:    map[string][]error{},
		gates:   map[string]chan struct{}{},
		calls:   map[string]int{},
		lastIdx: map[string]int{},
	}
	provider.RegisterApiProvider(p)
	if t != nil {
		t.Helper()
	}
	return p
}

// API implements provider.ApiProvider.
func (p *Provider) API() provider.Api { return p.api }

// Model returns a provider.Model bound to this mock's API type — pass it to
// agent pools, provider managers, or model factories under test.
func (p *Provider) Model(id string) provider.Model {
	return provider.Model{
		ID:         id,
		Name:       id,
		Api:        p.api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
		BaseURL:    "http://mock.invalid/v1/chat/completions",
	}
}

// Script appends a scripted turn for the given model ID. Repeated calls
// queue consecutive turns for multi-round conversations.
func (p *Provider) Script(modelID string, turn Turn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turns[modelID] = append(p.turns[modelID], turn)
}

// ReplyText is a convenience shorthand for Script: a single text reply
// (streamed as one delta) with StopReason end_turn.
func (p *Provider) ReplyText(modelID, text string) {
	p.Script(modelID, TextTurn(text))
}

// SetGate installs a channel for the model: the next Stream call for it
// streams any thinking/tool events, then blocks BEFORE the first text event
// until the channel is closed. Tests close the gate to release the role's
// reply after asserting concurrent UI state (e.g. a section stuck on
// "thinking..." while another role completes). One-shot: the gate is
// removed after the first stream passes through it.
func (p *Provider) SetGate(modelID string, gate chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates[modelID] = gate
}

// FailNext scripts the model's next Stream call to return err instead of a
// stream — the provider-400 class failure path (request rejected before any
// chunk). Errors are consumed FIFO ahead of the turn queue, so a model with
// both scripted fails and turns fails first, then streams. The failed call
// still increments Calls.
func (p *Provider) FailNext(modelID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[modelID] = append(p.errs[modelID], err)
}

// Calls returns how many Stream requests the given model has served.
func (p *Provider) Calls(modelID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[modelID]
}

// Stream implements provider.ApiProvider.
func (p *Provider) Stream(model provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	// Scripted failures are consumed first (FIFO): a provider-400 aborts the
	// request before any chunk streams.
	if err := p.nextErrLocked(model.ID); err != nil {
		p.calls[model.ID]++
		p.mu.Unlock()
		return nil, err
	}
	turn := p.nextTurnLocked(model.ID)
	gate := p.gates[model.ID]
	delete(p.gates, model.ID) // one-shot
	p.calls[model.ID]++
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		hold := turn.Hold
		if hold == nil {
			hold = gate
		}
		gateOpen := hold == nil
		for _, ev := range turn.Events {
			// Hold before the first TEXT event until released: thinking and
			// tool-call events stream through immediately so the test can
			// observe the "working/thinking" state, then release the reply.
			if !gateOpen && ev.Type == schema.EventTextStart {
				<-hold
				gateOpen = true
			}
			result.Push(ev)
		}
		if !gateOpen {
			<-hold // turn had no text events: hold before the final message
		}
		result.End(turn.Final)
	}()
	return result, nil
}

// StreamSimple implements provider.ApiProvider.
func (p *Provider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// nextErrLocked pops the model's next scripted Stream error, or nil when the
// error queue is empty.
func (p *Provider) nextErrLocked(modelID string) error {
	errs := p.errs[modelID]
	if len(errs) == 0 {
		return nil
	}
	p.errs[modelID] = errs[1:]
	return errs[0]
}

// nextTurnLocked pops the model's next scripted turn; the last scripted turn
// is replayed when the queue runs dry so tool-looping agents always get a
// well-formed reply.
func (p *Provider) nextTurnLocked(modelID string) Turn {
	turns := p.turns[modelID]
	if len(turns) == 0 {
		return TextTurn("mock reply from " + modelID)
	}
	idx := p.lastIdx[modelID]
	if idx >= len(turns) {
		idx = len(turns) - 1
	}
	p.lastIdx[modelID] = idx + 1
	return turns[idx]
}

// TextTurn builds a Turn that streams text as one delta and ends with
// StopReason end_turn.
func TextTurn(text string) Turn {
	return Turn{
		Events: []schema.AssistantMessageEvent{
			{Type: schema.EventTextStart, ContentIndex: 0},
			{Type: schema.EventTextDelta, ContentIndex: 0, Delta: text},
			{Type: schema.EventTextEnd, ContentIndex: 0, Content: text},
		},
		Final: &schema.AssistantMessage{
			Content:    []schema.ContentBlock{{Type: schema.ContentBlockText, Text: text}},
			StopReason: schema.StopReasonEndTurn,
		},
	}
}

// ThinkingTextTurn builds a Turn with a thinking block followed by text —
// exercises the TUI "thinking..." rendering path for sub-agents.
func ThinkingTextTurn(thinking, text string) Turn {
	return Turn{
		Events: []schema.AssistantMessageEvent{
			{Type: schema.EventThinkingStart, ContentIndex: 0},
			{Type: schema.EventThinkingDelta, ContentIndex: 0, Delta: thinking},
			{Type: schema.EventThinkingEnd, ContentIndex: 0, Content: thinking},
			{Type: schema.EventTextStart, ContentIndex: 1},
			{Type: schema.EventTextDelta, ContentIndex: 1, Delta: text},
			{Type: schema.EventTextEnd, ContentIndex: 1, Content: text},
		},
		Final: &schema.AssistantMessage{
			Content: []schema.ContentBlock{
				{Type: schema.ContentBlockThinking, Thinking: thinking},
				{Type: schema.ContentBlockText, Text: text},
			},
			StopReason: schema.StopReasonEndTurn,
		},
	}
}

// ToolCallTurn builds a Turn requesting one tool call; the agent under test
// will execute the tool and re-stream (consuming the model's next scripted
// turn — queue a TextTurn after this one to finish the conversation).
// argsJSON is the raw JSON arguments string (e.g. `{"path":"x.go"}`).
func ToolCallTurn(toolName, callID, argsJSON string) Turn {
	block := schema.ContentBlock{
		Type:          schema.ContentBlockToolCall,
		ToolCallID:    callID,
		ToolName:      toolName,
		ToolArguments: argsJSON,
	}
	return Turn{
		Events: []schema.AssistantMessageEvent{
			{Type: schema.EventToolCallStart, ContentIndex: 0, ToolCall: &block},
			{Type: schema.EventToolCallEnd, ContentIndex: 0, ToolCall: &block},
		},
		Final: &schema.AssistantMessage{
			Content:    []schema.ContentBlock{block},
			StopReason: schema.StopReasonToolCall,
		},
	}
}
