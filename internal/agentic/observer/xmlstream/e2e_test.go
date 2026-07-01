// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import (
	"bytes"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// ValidateXML checks if the XML is well-formed using the system's xmllint
func ValidateXML(t *testing.T, xmlContent string) {
	// Skip test if xmllint is not available
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not available, skipping XML validation")
	}

	// Write XML to temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.xml")
	if err := os.WriteFile(tmpFile, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("failed to write temp XML file: %v", err)
	}

	// Run xmllint
	cmd := exec.Command("xmllint", "--format", tmpFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("xmllint validation failed:\n%s\nXML content:\n%s", output, xmlContent)
	}
}

// bufferWriter implements StreamingXMLWriter for testing - collects all chunks
type bufferWriter struct {
	buf bytes.Buffer
}

func (bw *bufferWriter) WriteChunk(chunk string) error {
	bw.buf.WriteString(chunk)
	return nil
}

func (bw *bufferWriter) Close() error {
	return nil
}

func (bw *bufferWriter) String() string {
	return bw.buf.String()
}

// TestXMLSchema validates that the XML follows the expected structure
func TestXMLSchema(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "test-model",
		ConversationID: "test-schema",
		IncludeTimings: true,
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	// Simulate a simple conversation
	events := []agentic.OutputEvent{
		{Type: agentic.EventStateChange, State: agentic.StateContent},
		{Type: agentic.EventContent, State: agentic.StateContent, Text: "Hello"},
		{Type: agentic.EventToolCall, ToolName: "calculator", ToolInput: `{"expr":"2+2"}`},
		{Type: agentic.EventToolResult, Text: "4"},
		{Type: agentic.EventContent, State: agentic.StateContent, Text: "The answer is 4"},
		{Type: agentic.EventTokenStats, Timings: &agentic.TokenTimings{
			PromptN: 10, PredictedN: 20,
			PromptMs: 5.0, PredictedMs: 10.0,
		}},
		{Type: agentic.EventEnd},
	}

	for _, e := range events {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()

	// Validate with xmllint
	ValidateXML(t, result)

	// Validate schema structure
	if !strings.Contains(result, "<conversation>") {
		t.Error("missing conversation element")
	}
	if !strings.Contains(result, "</conversation>") {
		t.Error("missing closing conversation element")
	}
	if !strings.Contains(result, "<messages>") {
		t.Error("missing messages element")
	}
	if !strings.Contains(result, "</messages>") {
		t.Error("missing closing messages element")
	}
}

// TestXMLValid_ComplexSkillFlow tests XML validity with nested skill calls
func TestXMLValid_ComplexSkillFlow(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "test-model",
		ConversationID: "complex-skill-test",
		IncludeTimings: true,
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	// Simulate a conversation with skill calls (like the demo)
	events := []agentic.OutputEvent{
		// System message
		{Type: agentic.EventContent, Role: agentic.System, State: agentic.StateContent, Text: "You are a helpful assistant."},

		// User message
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "What time is it?"},

		// Assistant response with thinking and tool call
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "I need to check the time"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Let me check"},

		// Skill call (run_skill) - starts nested conversation
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "run_skill", ToolInput: `{"skill_name":"timeofday","task":"current time"}`},

		// Skill sub-agent conversation
		{Type: agentic.EventContent, Role: agentic.System, State: agentic.StateContent, Text: "# Time of Day Skill"},
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "current time"},

		// Tool call inside skill
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "Running date command"},
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "run_command", ToolInput: `{"command":"date"}`},
		{Type: agentic.EventToolResult, Text: "Sat May  9 19:43:19 CEST 2026"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Sat May  9 19:43:19 CEST 2026"},

		// Skill result returned to main agent
		{Type: agentic.EventToolResult, Text: "Sat May  9 19:43:19 CEST 2026"},

		// Final assistant response
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "It's Sat May 9 19:43:19 CEST 2026"},
		{Type: agentic.EventEnd},
	}

	for _, e := range events {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()
	t.Logf("Generated XML:\n%s", result)

	// Validate with xmllint
	ValidateXML(t, result)
}

// TestXMLValid_AllBlockTypes tests that all block types produce valid XML
func TestXMLValid_AllBlockTypes(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "test-model",
		ConversationID: "all-blocks-test",
		IncludeTimings: true,
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	// Test all block types in one message
	events := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "Thinking block content"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Regular content block"},
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "test_tool", ToolInput: `{"test":true}`},
		{Type: agentic.EventToolResult, Text: "Tool result output"},
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "run_skill", ToolInput: `{"skill_name":"test-skill","task":"test"}`},
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "Inside skill conversation"},
		{Type: agentic.EventToolResult, Text: "Skill completed"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "After skill"},
		{Type: agentic.EventTokenStats, Timings: &agentic.TokenTimings{
			PromptN: 100, PredictedN: 50,
			PromptMs: 10.0, PredictedMs: 20.0,
		}},
		{Type: agentic.EventEnd},
	}

	for _, e := range events {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()
	t.Logf("Generated XML:\n%s", result)

	// Validate with xmllint
	ValidateXML(t, result)

	// Verify all expected elements are present
	if !strings.Contains(result, "<thinking>") {
		t.Error("missing thinking block")
	}
	if !strings.Contains(result, "<content>") {
		t.Error("missing content block")
	}
	if !strings.Contains(result, "<toolcall>") {
		t.Error("missing toolcall block")
	}
	if !strings.Contains(result, "<skillcall>") {
		t.Error("missing skillcall block")
	}
	// Stats may not be present if not triggered in this sequence
}

// TestXMLValid_ParallelMessageTransitions tests role changes
func TestXMLValid_ParallelMessageTransitions(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "test-model",
		ConversationID: "transitions-test",
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	// Simulate multiple message transitions
	events := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.System, State: agentic.StateContent, Text: "System prompt"},
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "User question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "Thinking..."},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Response"},
		{Type: agentic.EventEnd},

		// Second turn
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "Another question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Another answer"},
		{Type: agentic.EventEnd},
	}

	for _, e := range events {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()
	t.Logf("Generated XML:\n%s", result)

	// Validate with xmllint
	ValidateXML(t, result)
}

// TestXMLValid_EmptyAndEdgeCases tests edge cases
func TestXMLValid_EmptyAndEdgeCases(t *testing.T) {
	t.Run("empty content", func(t *testing.T) {
		sw := &bufferWriter{}
		obs, _ := NewXMLStreamingObserver(Config{
			Writer: sw, Model: "test", ConversationID: "empty",
		})
		obs.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})
		obs.Flush()

		result := sw.String()
		ValidateXML(t, result)
	})

	t.Run("tool call without result", func(t *testing.T) {
		sw := &bufferWriter{}
		obs, _ := NewXMLStreamingObserver(Config{
			Writer: sw, Model: "test", ConversationID: "tool-only",
		})
		events := []agentic.OutputEvent{
			{Type: agentic.EventContent, State: agentic.StateContent, Text: "Before"},
			{Type: agentic.EventToolCall, ToolName: "test", ToolInput: `{}`},
			{Type: agentic.EventContent, State: agentic.StateContent, Text: "After"},
			{Type: agentic.EventEnd},
		}
		for _, e := range events {
			obs.OnEvent(e)
		}
		obs.Flush()

		result := sw.String()
		ValidateXML(t, result)
	})

	t.Run("multiple tool calls in sequence", func(t *testing.T) {
		sw := &bufferWriter{}
		obs, _ := NewXMLStreamingObserver(Config{
			Writer: sw, Model: "test", ConversationID: "multi-tool",
		})
		events := []agentic.OutputEvent{
			{Type: agentic.EventContent, State: agentic.StateContent, Text: "First"},
			{Type: agentic.EventToolCall, ToolName: "tool1", ToolInput: `{}`},
			{Type: agentic.EventToolResult, Text: "result1"},
			{Type: agentic.EventContent, State: agentic.StateContent, Text: "Second"},
			{Type: agentic.EventToolCall, ToolName: "tool2", ToolInput: `{}`},
			{Type: agentic.EventToolResult, Text: "result2"},
			{Type: agentic.EventEnd},
		}
		for _, e := range events {
			obs.OnEvent(e)
		}
		obs.Flush()

		result := sw.String()
		ValidateXML(t, result)
	})
}

// TestXMLParse validates the generated XML can be parsed back
func TestXMLParse(t *testing.T) {
	sw := &bufferWriter{}

	obs, err := NewXMLStreamingObserver(Config{
		Writer:         sw,
		Model:          "test-model",
		ConversationID: "parse-test",
		IncludeTimings: true,
	})
	if err != nil {
		t.Fatalf("failed to create observer: %v", err)
	}

	events := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.System, State: agentic.StateContent, Text: "System"},
		{Type: agentic.EventContent, Role: agentic.User, State: agentic.StateContent, Text: "User"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "Thinking"},
		{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "tool", ToolInput: `{"key":"value"}`},
		{Type: agentic.EventToolResult, Text: "output"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Response"},
		{Type: agentic.EventTokenStats, Timings: &agentic.TokenTimings{
			PromptN: 10, PredictedN: 20,
			PromptMs: 5.0, PredictedMs: 10.0,
		}},
		{Type: agentic.EventEnd},
	}

	for _, e := range events {
		obs.OnEvent(e)
	}

	obs.Flush()

	result := sw.String()

	// Try to parse the XML
	conv := struct {
		XMLName  xml.Name `xml:"conversation"`
		Metadata struct {
			ID    string `xml:"id"`
			Model string `xml:"model"`
			Start string `xml:"start"`
		} `xml:"metadata"`
		Messages []struct {
			Role   string `xml:"role"`
			Blocks []struct {
				Content  string `xml:"content"`
				Thinking string `xml:"thinking"`
			} `xml:"blocks"`
		} `xml:"messages>message"`
	}{}

	if err := xml.Unmarshal([]byte(result), &conv); err != nil {
		t.Fatalf("XML parse error: %v\nXML content:\n%s", err, result)
	}

	// Verify parsed content
	if conv.Metadata.ID != "parse-test" {
		t.Errorf("expected ID 'parse-test', got '%s'", conv.Metadata.ID)
	}
	if conv.Metadata.Model != "test-model" {
		t.Errorf("expected Model 'test-model', got '%s'", conv.Metadata.Model)
	}
	// Content may be split across blocks in a single message
	if len(conv.Messages) < 1 {
		t.Errorf("expected at least 1 message, got %d", len(conv.Messages))
	}
}
