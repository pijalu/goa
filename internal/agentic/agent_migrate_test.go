// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// ---------------------------------------------------------------------------
// P6 — provider schema projection in migrateSchemas
// ---------------------------------------------------------------------------

func TestMigrateSchemas_OpenAIKeepsSchemas(t *testing.T) {
	schemas := []ToolSchema{{
		Name:        "read",
		Description: "read a file",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "null"},
				}},
			},
		},
	}}
	model := provider.Model{Provider: provider.ProviderOpenAI, Api: provider.ApiOpenAICompletions, BaseURL: "https://api.openai.com/v1"}

	got := migrateSchemas(schemas, model)
	if len(got) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].InputSchema, schemas[0].Schema) {
		t.Errorf("OpenAI migrateSchemas must keep the input schema byte-identical:\n got %#v\nwant %#v", got[0].InputSchema, schemas[0].Schema)
	}
}

func TestMigrateSchemas_GeminiProjectsSchema(t *testing.T) {
	schemas := []ToolSchema{{
		Name:        "read",
		Description: "read a file",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"count": map[string]any{"anyOf": []any{
					map[string]any{"type": "integer"},
					map[string]any{"type": "null"},
				}},
			},
		},
	}}
	model := provider.Model{Provider: provider.ProviderGoogle, Api: provider.ApiGoogleGenerativeAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta"}

	got := migrateSchemas(schemas, model)
	if len(got) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(got))
	}
	input := got[0].InputSchema
	if _, ok := input["additionalProperties"]; ok {
		t.Errorf("Gemini migrateSchemas must drop additionalProperties, got %#v", input)
	}
	props, ok := input["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties lost: %#v", input)
	}
	count, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatalf("count lost: %#v", props)
	}
	if _, ok := count["anyOf"]; ok {
		t.Errorf("nullable anyOf must flatten after null arm strip, got %#v", count)
	}
	if typ, _ := count["type"].(string); typ != "integer" {
		t.Errorf("flattened count type = %q, want integer", typ)
	}
}

// ---------------------------------------------------------------------------
// P7 — per-turn tool collapse
// ---------------------------------------------------------------------------

// stopTurnTool returns a StopTurn result so the agent loop must produce the
// model's summary in a text-only round.
type stopTurnTool struct{}

func (stopTurnTool) Schema() ToolSchema {
	return ToolSchema{Name: "stop_it", Description: "stop the turn", Schema: map[string]any{"type": "object"}}
}
func (stopTurnTool) Execute(input string) (string, error) { return "stopped", nil }
func (stopTurnTool) IsRetryable(err error) bool           { return false }
func (stopTurnTool) ExecuteWithResult(input string) (ToolResult, error) {
	return ToolResult{Output: "stopped", StopTurn: true}, nil
}

// recordingStreamProvider records every provider context so tests can assert
// the NoTools collapse flag per round. Round 0 replays the given events; every
// later round streams a plain text answer (no more tool calls).
type recordingStreamProvider struct {
	api    provider.Api
	events []provider.AssistantMessageEvent
	ctxs   []provider.Context
	round  int
}

func (p *recordingStreamProvider) API() provider.Api { return p.api }

func (p *recordingStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.ctxs = append(p.ctxs, ctx)
	// Claim the round synchronously so later Stream calls never observe a
	// stale round counter from an in-flight goroutine.
	round := p.round
	p.round++
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if round > 0 {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Summary."})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Summary."}},
				StopReason: provider.StopReasonEndTurn,
			})
			return
		}
		for _, e := range p.events {
			result.Push(e)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *recordingStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// toolCallEvent builds a native tool-call stream event.
func toolCallEvent(id, name, args string) provider.AssistantMessageEvent {
	return provider.AssistantMessageEvent{
		Type: provider.EventToolCallEnd,
		ToolCall: &provider.ContentBlock{
			Type:          provider.ContentBlockToolCall,
			ToolCallID:    id,
			ToolName:      name,
			ToolArguments: args,
		},
	}
}

// registerRecordingProvider registers a recordingStreamProvider under a unique
// API id (safe across repeated test runs) and returns it.
func registerRecordingProvider(name string, events []provider.AssistantMessageEvent) *recordingStreamProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &recordingStreamProvider{
		api:    provider.Api("rec-" + name + "-" + fmt.Sprint(uniqueID)),
		events: events,
	}
	provider.RegisterApiProvider(p)
	return p
}

// TestStopTurnCollapse_NextRoundNoTools verifies P7: a StopTurn tool result
// keeps the turn alive for ONE final text-only round (no tools), then the
// turn ends; a subsequent turn restores the full tool set.
func TestStopTurnCollapse_NextRoundNoTools(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "stop_it", `{}`)}
	p := registerRecordingProvider("stop-collapse", events)
	mdl := testModel(p.api)

	agent := NewAgent(Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{stopTurnTool{}},
	})
	if _, err := agent.RunAndCollect(context.Background(), "complete it"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.ctxs) != 2 {
		t.Fatalf("expected 2 stream rounds (tool batch + text-only summary), got %d", len(p.ctxs))
	}
	if p.ctxs[0].NoTools {
		t.Error("round 0 (tool batch) must still carry tools")
	}
	if !p.ctxs[1].NoTools {
		t.Error("round 1 (after StopTurn) must be collapsed: NoTools=true")
	}
	if len(p.ctxs[1].Tools) != 0 {
		// The context still carries the schemas; the wire builder drops them.
		// NoTools is the collapse signal.
		t.Logf("context still carries %d tool schemas (expected; wire builder drops them)", len(p.ctxs[1].Tools))
	}
}

// TestStopTurnCollapse_NextTurnRestores verifies the collapse flag is
// per-round: after a StopTurn turn ends, the next user turn streams with the
// full tool set again.
func TestStopTurnCollapse_NextTurnRestores(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "stop_it", `{}`)}
	p := registerRecordingProvider("stop-restore", events)
	mdl := testModel(p.api)

	agent := NewAgent(Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{stopTurnTool{}},
	})
	if _, err := agent.RunAndCollect(context.Background(), "complete it"); err != nil {
		t.Fatalf("Run turn 1: %v", err)
	}
	if _, err := agent.RunAndCollect(context.Background(), "next turn"); err != nil {
		t.Fatalf("Run turn 2: %v", err)
	}

	// Turn 1: round 0 (tools) + round 1 (collapsed summary). Turn 2: round 0.
	if len(p.ctxs) != 3 {
		t.Fatalf("expected 3 stream rounds across two turns, got %d", len(p.ctxs))
	}
	if !p.ctxs[1].NoTools {
		t.Error("turn 1 round 1 must be collapsed (NoTools=true)")
	}
	if p.ctxs[2].NoTools {
		t.Error("turn 2 round 0 must restore the full tool set (NoTools=false)")
	}
}

// TestRecoveryStream_Collapsed verifies P7 recovery: when the consecutive
// tool-round limit is reached, the recovery request is text-only.
func TestRecoveryStream_Collapsed(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "stop_it", `{}`)}
	p := registerRecordingProvider("recovery-collapse", events)
	mdl := testModel(p.api)

	agent := NewAgent(Config{
		Model:                    mdl,
		SystemPrompt:             "test",
		Tools:                    []Tool{stopTurnTool{}},
		MaxConsecutiveToolRounds: 1,
	})
	if _, err := agent.RunAndCollect(context.Background(), "keep working"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.ctxs) != 2 {
		t.Fatalf("expected tool round + recovery round, got %d", len(p.ctxs))
	}
	if !p.ctxs[1].NoTools {
		t.Error("recovery request must be collapsed: NoTools=true")
	}
}
