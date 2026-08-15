// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// recordingSpillPolicy records invocations and applies a fixed replacement.
type recordingSpillPolicy struct {
	calls     int
	lastTool  string
	lastInput string
	replacer  func(result string) string
}

func (p *recordingSpillPolicy) ApplySpill(toolName, result string) string {
	p.calls++
	p.lastTool = toolName
	p.lastInput = result
	if p.replacer != nil {
		return p.replacer(result)
	}
	return result
}

func spillToolCall(id, name string) provider.ContentBlock {
	return provider.ContentBlock{
		Type:       provider.ContentBlockToolCall,
		ToolCallID: id,
		ToolName:   name,
	}
}

func TestSpillHook_OverCapResultReplaced(t *testing.T) {
	pol := &recordingSpillPolicy{replacer: func(result string) string {
		return result[:10] + "\n\n(Omitted bytes…)"
	}}
	a := NewAgent(Config{
		Model:       testModel(provider.ApiOpenAICompletions),
		SpillPolicy: pol,
	})

	big := strings.Repeat("x", 200000)
	call := spillToolCall("c1", "bash")
	results := map[string]ToolCallResult{"c1": {CallID: "c1", Output: big}}

	got := a.resolveToolResultContent(call, results)
	if pol.calls != 1 {
		t.Fatalf("spill policy should be invoked once, got %d", pol.calls)
	}
	if pol.lastTool != "bash" {
		t.Errorf("policy should see the tool name, got %q", pol.lastTool)
	}
	if pol.lastInput != big {
		t.Error("policy should receive the full untruncated result")
	}
	if !strings.Contains(got, "(Omitted bytes…)") {
		t.Errorf("model-facing content should be the policy replacement, got %d bytes", len(got))
	}
}

func TestSpillHook_ErrorResultsNeverSpilled(t *testing.T) {
	pol := &recordingSpillPolicy{}
	a := NewAgent(Config{
		Model:       testModel(provider.ApiOpenAICompletions),
		SpillPolicy: pol,
	})

	call := spillToolCall("c1", "bash")
	results := map[string]ToolCallResult{
		"c1": {CallID: "c1", Output: strings.Repeat("x", 200000), Err: errors.New("boom")},
	}
	got := a.resolveToolResultContent(call, results)
	if pol.calls != 0 {
		t.Error("error results must never reach the spill policy")
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("error result should surface the error text, got %q", got[:min(80, len(got))])
	}
}

func TestSpillHook_RunsBeforeHardTruncation(t *testing.T) {
	// A spilled (bounded) replacement must not then be mangled by the legacy
	// size-limit truncation: the spill output is already within budget.
	replacement := "head\n\n(Omitted 5 bytes. Full result stored at: /tmp/x. …)"
	pol := &recordingSpillPolicy{replacer: func(string) string { return replacement }}
	a := NewAgent(Config{
		Model:       testModel(provider.ApiOpenAICompletions),
		SpillPolicy: pol,
	})

	call := spillToolCall("c1", "bash")
	results := map[string]ToolCallResult{"c1": {CallID: "c1", Output: strings.Repeat("y", 200000)}}
	got := a.resolveToolResultContent(call, results)
	if got != replacement {
		t.Errorf("spill replacement should pass through untouched, got: %q", got[:min(80, len(got))])
	}
}

func TestSpillHook_NilPolicyKeepsLegacyTruncation(t *testing.T) {
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	call := spillToolCall("c1", "bash")
	results := map[string]ToolCallResult{"c1": {CallID: "c1", Output: strings.Repeat("y", 200000)}}
	got := a.resolveToolResultContent(call, results)
	if !strings.Contains(got, "Tool result was truncated") {
		t.Errorf("without a spill policy the legacy truncation still applies; got %d bytes", len(got))
	}
}
