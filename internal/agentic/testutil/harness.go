// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package testutil

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// ToolError captures a tool error that occurred during a conversation.
type ToolError struct {
	ToolName      string
	ErrorMsg      string
	HasEnrichment bool
}

// ConversationResult holds the outcome of a conversation turn.
type ConversationResult struct {
	Messages      []agentic.Message
	ToolErrors    []ToolError
	HasEnrichment bool
}

// AgentHarness creates and runs agents for testing, capturing tool errors
// via an EventEnd observer.
type AgentHarness struct {
	Agent      *agentic.Agent
	Model      provider.Model
	StreamOpts provider.StreamOptions
	Tools      []agentic.Tool
	toolErrors []ToolError
	messages   []agentic.Message
	mu         sync.Mutex
	done       chan struct{}
}

// NewSimulated creates a harness with a simulated LLM.
// The simulated provider is registered as an API provider.
func NewSimulated(t *testing.T, tools []agentic.Tool, responses []SimulatedResponse) *AgentHarness {
	sim := NewSimulatedProvider(responses)
	provider.RegisterApiProvider(sim)
	mdl := provider.Model{
		ID:         "test-model",
		Name:       "test-model",
		Api:        sim.API(),
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
	}
	return NewHarness(mdl, provider.StreamOptions{}, tools)
}

// NewHarness creates a harness with the given model, stream options, and tools.
func NewHarness(mdl provider.Model, opts provider.StreamOptions, tools []agentic.Tool) *AgentHarness {
	h := &AgentHarness{
		Model:      mdl,
		StreamOpts: opts,
		Tools:      tools,
		done:       make(chan struct{}),
	}

	h.Agent = agentic.NewAgent(agentic.Config{
		Model:         mdl,
		StreamOptions: opts,
		SystemPrompt:  "You are a test agent.",
		Tools:         tools,
		Logger:        agentic.NewLogger(agentic.Error),
	})

	h.Agent.AddObserver(h)

	return h
}

// OnEvent implements agentic.OutputObserver.
func (h *AgentHarness) OnEvent(event agentic.OutputEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if event.Type == agentic.EventToolResult {
		msg := agentic.Message{
			Type:       agentic.Content,
			Role:       agentic.ToolRole,
			Content:    event.Text,
			ToolCallID: event.ToolCallID,
		}
		h.messages = append(h.messages, msg)

		if strings.Contains(event.Text, "Error:") {
			te := ToolError{
				ErrorMsg:      event.Text,
				HasEnrichment: strings.Contains(event.Text, "Hint:"),
			}
			if event.ToolName != "" {
				te.ToolName = event.ToolName
			}
			h.toolErrors = append(h.toolErrors, te)
		}
	}

	if event.Type == agentic.EventEnd {
		close(h.done)
	}
}

// RunConversation sends user input and waits for the turn to complete.
func (h *AgentHarness) RunConversation(ctx context.Context, input string) (*ConversationResult, error) {
	h.mu.Lock()
	h.toolErrors = nil
	h.messages = nil
	h.done = make(chan struct{})
	h.mu.Unlock()

	err := h.Agent.Run(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}

	select {
	case <-h.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	result := &ConversationResult{
		Messages:   append([]agentic.Message(nil), h.messages...),
		ToolErrors: append([]ToolError(nil), h.toolErrors...),
	}

	for _, te := range h.toolErrors {
		if te.HasEnrichment {
			result.HasEnrichment = true
			break
		}
	}

	return result, nil
}
