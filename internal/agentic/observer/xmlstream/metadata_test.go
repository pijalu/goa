// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import (
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// TestXMLValid_MetadataSkillFlow simulates the exact demo flow with metadata
func TestXMLValid_MetadataSkillFlow(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "local-model",
		ConversationID: "stream-xml-demo",
		IncludeTimings: true,
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	// Simulate first Run: "Hello, what can you do?"
	events1 := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "Hello, what can you do?"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "I can help with various tasks including checking the time."},
		{Type: agentic.EventEnd},
	}
	for _, e := range events1 {
		obs.OnEvent(e)
	}

	// Simulate second RunWithMetadata: "What time is it?" with metadata
	events2 := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "What time is it?", Metadata: map[string]string{"category": "demo", "internal": "true"}},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "The user wants to know the current time."},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "I'll check the time for you."},
		// Skill call
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "run_skill", ToolInput: `{"skill_name":"timeofday","task":"current time"}`},
		// Sub-agent events
		{Type: agentic.EventContent, Role: agentic.System, State: agentic.StateContent, Text: "You are a time skill."},
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "current time"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "The current time of day is 14:20:51."},
		{Type: agentic.EventTokenStats, Timings: &agentic.TokenTimings{PromptN: 42, PredictedN: 10, PromptMs: 100.0, PredictedMs: 3583.24}},
		{Type: agentic.EventEnd},
		// Skill result
		{Type: agentic.EventToolResult, Text: "The current time of day is 14:20:51."},
		// Final assistant response
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "The current time is 14:20:51."},
		{Type: agentic.EventEnd},
	}
	for _, e := range events2 {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()
	t.Logf("Generated XML:\n%s", result)

	// Validate with xmllint
	ValidateXML(t, result)
}
