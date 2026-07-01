// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
)

func TestTransformMessages_NoChangeForSameModel(t *testing.T) {
	model := Model{
		ID:         "gpt-4o",
		Api:        ApiOpenAICompletions,
		Provider:   ProviderOpenAI,
		InputTypes: []string{"text", "image"},
	}

	// Source fields match target model → messages pass through unchanged.
	msgs := []Message{
		NewUserMessage("hello"),
		{
			Role:           RoleAssistant,
			Content:        []ContentBlock{{Type: ContentBlockText, Text: "hi there"}},
			SourceProvider: ProviderOpenAI,
			SourceAPI:      ApiOpenAICompletions,
			SourceModelID:  "gpt-4o",
		},
	}

	result := TransformMessages(msgs, model, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Content[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", result[0].Content[0].Text)
	}
}

func TestTransformMessages_DowngradeImagesNonVision(t *testing.T) {
	model := Model{
		ID:         "gpt-4o-mini",
		Api:        ApiOpenAICompletions,
		Provider:   ProviderOpenAI,
		InputTypes: []string{"text"}, // no "image"
	}

	msgs := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: ContentBlockText, Text: "describe this"},
				{Type: ContentBlockImage, ImageData: "base64...", ImageMimeType: "image/png"},
			},
		},
	}

	result := TransformMessages(msgs, model, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if len(result[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result[0].Content))
	}
	if result[0].Content[0].Type != ContentBlockText {
		t.Errorf("expected first block to be text")
	}
	if result[0].Content[1].Type != ContentBlockText {
		t.Errorf("expected image to be downgraded to text, got %q", result[0].Content[1].Type)
	}
	if result[0].Content[1].Text != nonVisionUserImagePlaceholder {
		t.Errorf("expected placeholder text, got %q", result[0].Content[1].Text)
	}
}

func TestTransformMessages_ConsecutiveImagesCollapsed(t *testing.T) {
	model := Model{
		ID:         "text-only",
		Api:        ApiOpenAICompletions,
		Provider:   ProviderOpenAI,
		InputTypes: []string{"text"},
	}

	msgs := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: ContentBlockText, Text: "imagine"},
				{Type: ContentBlockImage, ImageData: "a"},
				{Type: ContentBlockImage, ImageData: "b"},
				{Type: ContentBlockText, Text: "done"},
			},
		},
	}

	result := TransformMessages(msgs, model, nil)
	blocks := result[0].Content
	t.Logf("blocks: %d", len(blocks))
	for i, b := range blocks {
		t.Logf("  [%d] type=%s text=%q", i, b.Type, b.Text)
	}
	// Text block, then single placeholder, then text block
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks (text, placeholder, text), got %d", len(blocks))
	}
}

func TestTransformMessages_ThinkingToText(t *testing.T) {
	model := Model{
		ID:       "other-model",
		Api:      ApiOpenAICompletions,
		Provider: ProviderOpenAI,
	}

	msgs := []Message{
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockThinking, Thinking: "I need to think about this"},
			{Type: ContentBlockText, Text: "Here is my answer"},
		}),
	}

	result := TransformMessages(msgs, model, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	blocks := result[0].Content
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	// Thinking should be converted to text
	if blocks[0].Type != ContentBlockText {
		t.Errorf("expected thinking converted to text, got %q", blocks[0].Type)
	}
	if blocks[0].Text != "I need to think about this" {
		t.Errorf("expected thinking content as text, got %q", blocks[0].Text)
	}
	if blocks[1].Type != ContentBlockText {
		t.Errorf("expected text block, got %q", blocks[1].Type)
	}
}

func TestTransformMessages_DropEmptyThinking(t *testing.T) {
	model := Model{
		ID:       "other-model",
		Api:      ApiOpenAICompletions,
		Provider: ProviderOpenAI,
	}

	msgs := []Message{
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockThinking, Thinking: ""},
			{Type: ContentBlockText, Text: "answer"},
		}),
	}

	result := TransformMessages(msgs, model, nil)
	blocks := result[0].Content
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block (empty thinking dropped), got %d", len(blocks))
	}
	if blocks[0].Text != "answer" {
		t.Errorf("expected 'answer', got %q", blocks[0].Text)
	}
}

func TestTransformMessages_RedactedThinkingDroppedCrossModel(t *testing.T) {
	model := Model{
		ID:       "other-model",
		Api:      ApiOpenAICompletions,
		Provider: ProviderOpenAI,
	}

	msgs := []Message{
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockThinking, Thinking: "encrypted", Redacted: true, ThinkingSignature: "sig123"},
			{Type: ContentBlockText, Text: "visible"},
		}),
	}

	result := TransformMessages(msgs, model, nil)
	blocks := result[0].Content
	// Redacted thinking should be dropped for cross-model
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block (redacted thinking dropped), got %d", len(blocks))
	}
}

func TestTransformMessages_SynthesizeOrphanedToolCalls(t *testing.T) {
	model := Model{
		ID:         "gpt-4o",
		Api:        ApiOpenAICompletions,
		Provider:   ProviderOpenAI,
		InputTypes: []string{"text"},
	}

	msgs := []Message{
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockToolCall, ToolCallID: "call_1", ToolName: "get_weather", ToolArguments: `{"city":"Paris"}`},
		}),
		NewUserMessage("what now?"),
	}

	result := TransformMessages(msgs, model, nil)
	// Should insert synthetic tool result between assistant and user
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (assistant, synthetic tool result, user), got %d", len(result))
	}
	if result[1].Role != RoleToolResult {
		t.Errorf("expected synthetic tool result, got role=%q", result[1].Role)
	}
	if !result[1].Content[0].IsError {
		t.Error("expected synthetic tool result to be IsError")
	}
}

func TestTransformMessages_SkipErroredAssistant(t *testing.T) {
	model := Model{
		ID:       "gpt-4o",
		Api:      ApiOpenAICompletions,
		Provider: ProviderOpenAI,
	}

	msgs := []Message{
		NewUserMessage("hello"),
		{
			Role:       RoleAssistant,
			Content:    []ContentBlock{{Type: ContentBlockText, Text: "partial response"}},
			StopReason: StopReasonError,
		},
		NewUserMessage("retry"),
	}

	result := TransformMessages(msgs, model, nil)
	// The errored assistant message should be removed
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user, user), got %d", len(result))
	}
}

func TestTransformMessages_ToolCallIDNormalization(t *testing.T) {
	model := Model{
		ID:       "target-model",
		Api:      ApiOpenAICompletions,
		Provider: ProviderOpenAI,
	}

	normalizer := func(id string, _ Model, _ Message) string {
		return "norm_" + id
	}

	msgs := []Message{
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockToolCall, ToolCallID: "call_abc", ToolName: "search", ToolArguments: `{}`},
		}),
		{
			Role: RoleToolResult,
			Content: []ContentBlock{
				{Type: ContentBlockToolResult, ToolCallID: "call_abc", ToolName: "search", Text: "result"},
			},
		},
	}

	result := TransformMessages(msgs, model, normalizer)
	if result[0].Content[0].ToolCallID != "norm_call_abc" {
		t.Errorf("expected normalized tool call ID 'norm_call_abc', got %q", result[0].Content[0].ToolCallID)
	}
	if result[1].Content[0].ToolCallID != "norm_call_abc" {
		t.Errorf("expected normalized tool result ID 'norm_call_abc', got %q", result[1].Content[0].ToolCallID)
	}
}

func TestTransformMessages_PreservesContentOrder(t *testing.T) {
	model := Model{
		ID:         "gpt-4o",
		Api:        ApiOpenAICompletions,
		Provider:   ProviderOpenAI,
		InputTypes: []string{"text"},
	}

	msgs := []Message{
		NewUserMessage("first"),
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockText, Text: "a"},
		}),
		NewUserMessage("second"),
		NewAssistantMessage([]ContentBlock{
			{Type: ContentBlockText, Text: "b"},
		}),
	}

	result := TransformMessages(msgs, model, nil)
	if len(result) != 4 {
		t.Fatalf("expected 4 unchanged messages, got %d", len(result))
	}
	if result[2].Content[0].Text != "second" {
		t.Errorf("expected 'second', got %q", result[2].Content[0].Text)
	}
}

func TestIsVisionModel(t *testing.T) {
	vision := Model{InputTypes: []string{"text", "image"}}
	if !IsVisionModel(vision) {
		t.Error("expected vision model")
	}

	text := Model{InputTypes: []string{"text"}}
	if IsVisionModel(text) {
		t.Error("expected non-vision model")
	}

	empty := Model{}
	if IsVisionModel(empty) {
		t.Error("expected empty to be non-vision")
	}
}

func TestDowngradeImagesInContent(t *testing.T) {
	placeholder := "(omitted)"
	content := []ContentBlock{
		{Type: ContentBlockText, Text: "a"},
		{Type: ContentBlockImage, ImageData: "img1", ImageMimeType: "image/png"},
		{Type: ContentBlockImage, ImageData: "img2", ImageMimeType: "image/jpeg"},
		{Type: ContentBlockText, Text: "b"},
	}
	result := downgradeImagesInContent(content, placeholder)
	if len(result) != 3 {
		t.Fatalf("expected 3 blocks (text, placeholder, text), got %d", len(result))
	}
	if result[1].Type != ContentBlockText {
		t.Errorf("expected placeholder as text block, got %q", result[1].Type)
	}
	if result[1].Text != placeholder {
		t.Errorf("expected placeholder text, got %q", result[1].Text)
	}
}

func TestIsBlank(t *testing.T) {
	if !isBlank("") {
		t.Error("empty should be blank")
	}
	if !isBlank("   ") {
		t.Error("spaces should be blank")
	}
	if !isBlank("\t\n ") {
		t.Error("whitespace should be blank")
	}
	if isBlank("not blank") {
		t.Error("non-blank should not be blank")
	}
}
