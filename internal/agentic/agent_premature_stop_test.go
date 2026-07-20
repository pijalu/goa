// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// prematureStopProvider simulates the deepseek/opencode-go premature-stop
// quirk: round 1 issues a real tool call, round 2 ends finish_reason=stop with
// a mid-sentence fragment (no terminal punctuation), round 3+ produces the
// final answer. Call count lets tests assert whether goa auto-continued.
type prematureStopProvider struct {
	api   provider.Api
	calls atomic.Int32
	// fragment is the mid-sentence content emitted on round 2.
	fragment string
	// alwaysTruncate, when true, keeps emitting fragments (tests the cap).
	alwaysTruncate bool
}

func (p *prematureStopProvider) API() provider.Api { return p.api }

func (p *prematureStopProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		switch {
		case round == 1:
			// Tool call so the turn has real tool execution.
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_1",
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`}},
				StopReason: provider.StopReasonToolCall,
			})
		case round == 2 || p.alwaysTruncate:
			// Premature stop: mid-sentence fragment, no terminal punctuation.
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: p.fragment})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.fragment}},
				StopReason: provider.StopReasonEndTurn,
			})
		default:
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Both fixes applied and verified."})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Both fixes applied and verified."}},
				StopReason: provider.StopReasonEndTurn,
			})
		}
	}()
	return result, nil
}

func (p *prematureStopProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

var prematureStopCounter atomic.Int32

func registerPrematureStopProvider(fragment string, alwaysTruncate bool) *prematureStopProvider {
	id := prematureStopCounter.Add(1)
	p := &prematureStopProvider{
		api:            provider.Api(fmt.Sprintf("test-premature-stop-%d", id)),
		fragment:       fragment,
		alwaysTruncate: alwaysTruncate,
	}
	provider.RegisterApiProvider(p)
	return p
}

// TestAgent_PrematureStopAutoContinues verifies that a finish_reason=stop with
// mid-sentence content after real tool work is NOT treated as a turn end: goa
// detects the truncated output and auto-continues, so the model finishes the
// task without the user typing "continue" (bugs.md premature-stop).
func TestAgent_PrematureStopAutoContinues(t *testing.T) {
	p := registerPrematureStopProvider("Let me fix both the call site and the function:", false)
	agent := newAgentWithMockTool(p.API(), 10)
	obs := runAgentCollectingEvents(t, agent, "fix the union bug")

	// The turn must have auto-continued past the fragment and produced the
	// final answer.
	var allText strings.Builder
	for _, e := range obs.Events() {
		if e.Type == EventContent {
			allText.WriteString(e.Text)
		}
	}
	if !strings.Contains(allText.String(), "Both fixes applied and verified.") {
		t.Errorf("expected the final answer after auto-continue; got text: %q", allText.String())
	}
	if got := p.calls.Load(); got != 3 {
		t.Errorf("provider calls = %d, want 3 (tool round, truncated round, auto-continued round)", got)
	}
}

// TestAgent_CompleteStopNoAutoContinue verifies a normal, properly-terminated
// answer is not auto-continued (no false positive).
func TestAgent_CompleteStopNoAutoContinue(t *testing.T) {
	p := registerPrematureStopProvider("The fix is complete.", false)
	agent := newAgentWithMockTool(p.API(), 10)
	// Round 2's fragment here is a complete sentence, so the turn must end
	// after round 2 (no auto-continue).
	// Round 1 is the tool call; round 2 is the fragment → but it's complete,
	// so the turn ends there.
	runAgentCollectingEvents(t, agent, "fix it")
	if got := p.calls.Load(); got != 2 {
		t.Errorf("provider calls = %d, want 2 (no auto-continue for complete output)", got)
	}
}

// TestAgent_PrematureStopCapped verifies the auto-continue is bounded: a
// provider that keeps truncating is not looped forever.
func TestAgent_PrematureStopCapped(t *testing.T) {
	p := registerPrematureStopProvider("Still working on it:", true)
	agent := newAgentWithMockTool(p.API(), 10)
	runAgentCollectingEvents(t, agent, "fix it")
	// 1 tool round + 1 truncated round + up to maxAutoContinues retries.
	if got := p.calls.Load(); got > 2+int32(maxAutoContinuePerTurn) {
		t.Errorf("provider calls = %d, want <= %d (auto-continue must be capped)", got, 2+maxAutoContinuePerTurn)
	}
}

// TestLooksTruncated is a table-driven unit test for the fragment detector.
func TestLooksTruncated(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"The fix is complete.", false},
		{"Done!", false},
		{"Is it done?", false},
		{"All tests pass (clean run)", false},
		{"- item one\n- item two", true}, // no terminal punctuation (markdown list) — borderline, accepted
		{"| a | b |\n| 1 | 2 |", false},  // markdown table closes with |
		{"Let me fix both the call site and the function:", true},
		{"Still working on it:", true},
		{"I'll now update the parser", true},
		{"Now I need to check the executor", true},
		{"The result was 42", true},  // no terminal punctuation → treated truncated
		{"```go\nfunc main() {}\n```", false},
	}
	for _, c := range cases {
		if got := looksTruncated(c.in); got != c.want {
			t.Errorf("looksTruncated(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
