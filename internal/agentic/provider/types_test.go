// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
)

func TestApiConstants_AreUnique(t *testing.T) {
	seen := make(map[Api]bool)
	tests := []Api{
		ApiOpenAICompletions, ApiOpenAIResponses, ApiOpenAICodexResponses,
		ApiAzureOpenAIResponses, ApiAnthropicMessages, ApiGoogleGenerativeAI,
		ApiGoogleVertex, ApiMistralConversations, ApiBedrockConverse,
	}
	for _, api := range tests {
		if seen[api] {
			t.Errorf("duplicate Api constant: %q", api)
		}
		seen[api] = true
	}
}

func TestProviderConstants_AreUnique(t *testing.T) {
	seen := make(map[Provider]bool)
	tests := []Provider{
		ProviderOpenAI, ProviderAnthropic, ProviderGoogle, ProviderMistral,
		ProviderAWS, ProviderAzure, ProviderGitHub, ProviderTogether,
		ProviderFireworks, ProviderGroq, ProviderPerplexity, ProviderDeepSeek,
		ProviderOpenRouter, ProviderLMStudio, ProviderOllama, ProviderCustom,
	}
	for _, p := range tests {
		if seen[p] {
			t.Errorf("duplicate Provider constant: %q", p)
		}
		seen[p] = true
	}
}

func TestThinkingLevelConstants_AreUnique(t *testing.T) {
	seen := make(map[ThinkingLevel]bool)
	tests := []ThinkingLevel{
		ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium,
		ThinkingHigh, ThinkingXHigh,
	}
	for _, l := range tests {
		if seen[l] {
			t.Errorf("duplicate ThinkingLevel constant: %q", l)
		}
		seen[l] = true
	}
}

func TestCacheRetentionConstants(t *testing.T) {
	if CacheRetentionNone != "none" {
		t.Errorf("expected CacheRetentionNone=none, got %q", CacheRetentionNone)
	}
	if CacheRetentionShort != "short" {
		t.Errorf("expected CacheRetentionShort=short, got %q", CacheRetentionShort)
	}
	if CacheRetentionLong != "long" {
		t.Errorf("expected CacheRetentionLong=long, got %q", CacheRetentionLong)
	}
}

func TestTransportConstants(t *testing.T) {
	if TransportSSE != "sse" {
		t.Errorf("expected TransportSSE=sse, got %q", TransportSSE)
	}
	if TransportWebSocket != "websocket" {
		t.Errorf("expected TransportWebSocket=websocket, got %q", TransportWebSocket)
	}
}

func TestContentBlockTypeConstants(t *testing.T) {
	tests := []struct {
		actual ContentBlockType
		want   string
	}{
		{ContentBlockText, "text"},
		{ContentBlockThinking, "thinking"},
		{ContentBlockToolCall, "tool_call"},
		{ContentBlockToolResult, "tool_result"},
		{ContentBlockImage, "image"},
	}
	for _, tt := range tests {
		if string(tt.actual) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.actual))
		}
	}
}

func TestNewTextMessage(t *testing.T) {
	m := NewTextMessage(RoleUser, "hello")
	if m.Role != RoleUser {
		t.Errorf("expected Role=user, got %q", m.Role)
	}
	if len(m.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(m.Content))
	}
	if m.Content[0].Type != ContentBlockText {
		t.Errorf("expected ContentBlockType=text, got %q", m.Content[0].Type)
	}
	if m.Content[0].Text != "hello" {
		t.Errorf("expected Text=hello, got %q", m.Content[0].Text)
	}
}

func TestNewUserMessage(t *testing.T) {
	m := NewUserMessage("user text")
	if m.Role != RoleUser {
		t.Errorf("expected Role=user, got %q", m.Role)
	}
	if m.Content[0].Text != "user text" {
		t.Errorf("expected 'user text', got %q", m.Content[0].Text)
	}
}

func TestNewSystemMessage(t *testing.T) {
	m := NewSystemMessage("system prompt")
	if m.Role != RoleSystem {
		t.Errorf("expected Role=system, got %q", m.Role)
	}
	if m.Content[0].Text != "system prompt" {
		t.Errorf("expected 'system prompt', got %q", m.Content[0].Text)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentBlockText, Text: "hello"},
		{Type: ContentBlockToolCall, ToolCallID: "call_1", ToolName: "get_weather", ToolArguments: `{"city":"Paris"}`},
	}
	m := NewAssistantMessage(blocks)
	if m.Role != RoleAssistant {
		t.Errorf("expected Role=assistant, got %q", m.Role)
	}
	if len(m.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(m.Content))
	}
	if m.Content[1].ToolCallID != "call_1" {
		t.Errorf("expected ToolCallID=call_1, got %q", m.Content[1].ToolCallID)
	}
}

func TestNewToolResultMessage(t *testing.T) {
	m := NewToolResultMessage("call_1", "get_weather", `{"temp":22}`, false)
	if m.Role != RoleToolResult {
		t.Errorf("expected Role=tool_result, got %q", m.Role)
	}
	if len(m.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(m.Content))
	}
	cb := m.Content[0]
	if cb.Type != ContentBlockToolResult {
		t.Errorf("expected ContentBlockType=tool_result, got %q", cb.Type)
	}
	if cb.ToolCallID != "call_1" {
		t.Errorf("expected ToolCallID=call_1, got %q", cb.ToolCallID)
	}
	if cb.ToolName != "get_weather" {
		t.Errorf("expected ToolName=get_weather, got %q", cb.ToolName)
	}
	if cb.Text != `{"temp":22}` {
		t.Errorf("expected Text={'temp':22}, got %q", cb.Text)
	}
	if cb.IsError {
		t.Error("expected IsError=false")
	}
}

func TestNewToolResultMessage_Error(t *testing.T) {
	m := NewToolResultMessage("call_2", "search", "not found", true)
	if !m.Content[0].IsError {
		t.Error("expected IsError=true")
	}
}

func TestThinkingLevelMap_Type(t *testing.T) {
	tlm := ThinkingLevelMap{
		ThinkingLow:    "low",
		ThinkingMedium: "medium",
		ThinkingHigh:   "high",
	}
	if tlm[ThinkingLow] != "low" {
		t.Errorf("expected 'low', got %q", tlm[ThinkingLow])
	}
	if tlm[ThinkingMedium] != "medium" {
		t.Errorf("expected 'medium', got %q", tlm[ThinkingMedium])
	}
}

func TestThinkingBudgets_Type(t *testing.T) {
	tb := ThinkingBudgets{
		ThinkingLow:    1024,
		ThinkingMedium: 4096,
		ThinkingHigh:   16384,
	}
	if tb[ThinkingMedium] != 4096 {
		t.Errorf("expected 4096, got %d", tb[ThinkingMedium])
	}
}

func TestModelPricing_ZeroValue(t *testing.T) {
	var p ModelPricing
	if p.Input != 0 || p.Output != 0 || p.CacheRead != 0 || p.CacheWrite != 0 {
		t.Error("expected zero-value ModelPricing")
	}
}

func TestModel_Defaults(t *testing.T) {
	m := Model{}
	if m.ID != "" || m.Name != "" || m.Api != "" || m.Provider != "" {
		t.Error("expected zero-value Model")
	}
	if m.ContextWindow != 0 || m.MaxTokens != 0 {
		t.Error("expected zero-value numeric fields")
	}
}

func TestStopReasonConstants(t *testing.T) {
	tests := []struct {
		actual StopReason
		want   string
	}{
		{StopReasonEndTurn, "end_turn"},
		{StopReasonMaxTokens, "max_tokens"},
		{StopReasonStopSequence, "stop_sequence"},
		{StopReasonToolCall, "tool_call"},
		{StopReasonError, "error"},
		{StopReasonContentFiltered, "content_filtered"},
	}
	for _, tt := range tests {
		if string(tt.actual) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.actual))
		}
	}
}

func TestRoleConstants(t *testing.T) {
	tests := []struct {
		actual Role
		want   string
	}{
		{RoleSystem, "system"},
		{RoleUser, "user"},
		{RoleAssistant, "assistant"},
		{RoleToolResult, "tool_result"},
	}
	for _, tt := range tests {
		if string(tt.actual) != tt.want {
			t.Errorf("expected %q, got %q", tt.want, string(tt.actual))
		}
	}
}

func AssistantMessage_Clone(t *testing.T) {
	m := &AssistantMessage{
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: "hello"},
		},
		Usage:      &Usage{InputTokens: 10, OutputTokens: 20},
		StopReason: StopReasonEndTurn,
	}
	cp := m.Clone()
	if cp == nil {
		t.Fatal("Clone returned nil")
	}
	if len(cp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(cp.Content))
	}
	if cp.Content[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", cp.Content[0].Text)
	}
	if cp.Usage.InputTokens != 10 || cp.Usage.OutputTokens != 20 {
		t.Error("expected Usage to be cloned")
	}
	// Mutate original to verify deep copy
	m.Content[0].Text = "modified"
	if cp.Content[0].Text != "hello" {
		t.Error("Clone should be a deep copy — modifying original should not affect clone")
	}
}

func TestAssistantMessage_CloneNil(t *testing.T) {
	m := (*AssistantMessage)(nil)
	cp := m.Clone()
	if cp != nil {
		t.Error("expected nil clone for nil receiver")
	}
}

func TestStreamOptions_Defaults(t *testing.T) {
	var opts StreamOptions
	if opts.Temperature != nil {
		t.Error("expected nil Temperature")
	}
	if opts.MaxTokens != 0 {
		t.Errorf("expected MaxTokens=0, got %d", opts.MaxTokens)
	}
	if opts.Transport != "" {
		t.Errorf("expected empty Transport, got %q", opts.Transport)
	}
}

func TestSimpleStreamOptions_Embedding(t *testing.T) {
	opts := SimpleStreamOptions{
		StreamOptions: StreamOptions{
			MaxTokens: 1024,
		},
		Reasoning: ThinkingMedium,
	}
	if opts.MaxTokens != 1024 {
		t.Errorf("expected MaxTokens=1024, got %d", opts.MaxTokens)
	}
	if opts.Reasoning != ThinkingMedium {
		t.Errorf("expected Reasoning=medium, got %q", opts.Reasoning)
	}
}

func TestToolSchema_Defaults(t *testing.T) {
	var ts ToolSchema
	if ts.Name != "" {
		t.Errorf("expected empty Name, got %q", ts.Name)
	}
	if ts.InputSchema != nil {
		t.Error("expected nil InputSchema")
	}
}

func TestContentBlock_Defaults(t *testing.T) {
	var cb ContentBlock
	if cb.Type != "" {
		t.Errorf("expected empty Type, got %q", cb.Type)
	}
	if cb.Redacted {
		t.Error("expected Redacted=false")
	}
	if cb.IsError {
		t.Error("expected IsError=false")
	}
}

func TestUsage_ZeroValue(t *testing.T) {
	var u Usage
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Error("expected zero-value Usage")
	}
}
