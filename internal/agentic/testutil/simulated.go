// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package testutil

import (
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// SimulatedResponse describes a single tool call the simulated LLM will make.
type SimulatedResponse struct {
	ToolName  string // Name of the tool to call
	ToolInput string // JSON-encoded input for the tool
	Content   string // Optional content text before the tool call
}

// SimulatedTool is a tool that returns a predetermined result or error.
type SimulatedTool struct {
	ToolSchema agentic.ToolSchema
	Result     string
	Err        error
	Retryable  bool
	CallCount  int
}

// Schema implements agentic.Tool.
func (t *SimulatedTool) Schema() agentic.ToolSchema {
	return t.ToolSchema
}

// Execute implements agentic.Tool.
func (t *SimulatedTool) Execute(input string) (string, error) {
	t.CallCount++
	return t.Result, t.Err
}

// IsRetryable implements agentic.Tool.
func (t *SimulatedTool) IsRetryable(err error) bool {
	return t.Retryable
}

// MustSimulatedTool creates a SimulatedTool or panics on invalid schema.
func MustSimulatedTool(name, description string, result string, err error) *SimulatedTool {
	return &SimulatedTool{
		ToolSchema: agentic.ToolSchema{
			Name:        name,
			Description: description,
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Result: result,
		Err:    err,
	}
}

// SimulatedFailingTool creates a tool that always returns an error.
func SimulatedFailingTool(name, description, errMsg string) *SimulatedTool {
	return MustSimulatedTool(name, description, "", &ffmtError{errMsg})
}

type ffmtError struct{ msg string }

func (e *ffmtError) Error() string { return e.msg }

// SimulatedProvider is a test provider that uses the new stream-based API.
// It implements provider.ApiProvider and returns predetermined responses.
type SimulatedProvider struct {
	Responses []SimulatedResponse
	apiType   provider.Api // unique per instance
	mu        sync.Mutex
	index     int // consumed responses counter
}

var simulatedCounter int

// NewSimulatedProvider creates a provider with the given responses.
func NewSimulatedProvider(responses []SimulatedResponse) *SimulatedProvider {
	simulatedCounter++
	return &SimulatedProvider{
		Responses: responses,
		apiType:   provider.Api(fmt.Sprintf("test-simulated-%d", simulatedCounter)),
	}
}

// API returns a unique API type for this mock provider.
func (p *SimulatedProvider) API() provider.Api {
	return p.apiType
}

// Stream initiates a mock streaming response, consuming one response per call.
func (p *SimulatedProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		p.mu.Lock()
		if p.index >= len(p.Responses) {
			p.mu.Unlock()
			result.End(&provider.AssistantMessage{
				StopReason: provider.StopReasonEndTurn,
			})
			return
		}
		resp := p.Responses[p.index]
		p.index++
		p.mu.Unlock()

		if resp.Content != "" {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextStart,
				ContentIndex: 0,
			})
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextDelta,
				ContentIndex: 0,
				Delta:        resp.Content,
			})
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextEnd,
				ContentIndex: 0,
			})
		}
		if resp.ToolName != "" {
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventToolCallEnd,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolName:      resp.ToolName,
					ToolArguments: resp.ToolInput,
				},
			})
		}
		result.End(&provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{Type: provider.ContentBlockText, Text: "Mock response complete."},
			},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *SimulatedProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}
