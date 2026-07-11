// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"context"
	"time"
)

// ContentBlock is a single content block within a message.
type ContentBlock struct {
	Type ContentBlockType `json:"type"`

	Text string `json:"text,omitempty"`

	Thinking          string `json:"thinking,omitempty"`
	ThinkingSignature string `json:"thinking_signature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`

	ToolCallID    string `json:"tool_call_id,omitempty"`
	ToolName      string `json:"tool_name,omitempty"`
	ToolArguments string `json:"tool_arguments,omitempty"`
	IsError       bool   `json:"is_error,omitempty"`

	ImageData     string `json:"image_data,omitempty"`
	ImageMimeType string `json:"image_mime_type,omitempty"`
}

// Message is a discriminated message in a conversation.
type Message struct {
	Role       Role           `json:"role"`
	Content    []ContentBlock `json:"content"`
	Usage      *Usage         `json:"usage,omitempty"`
	StopReason StopReason     `json:"stop_reason,omitempty"`

	// Extra holds hook-specific metadata that does not belong in the canonical
	// message fields (e.g., cache control markers).
	Extra map[string]interface{} `json:"extra,omitempty"`

	SourceProvider Provider `json:"source_provider,omitempty"`
	SourceAPI      Api      `json:"source_api,omitempty"`
	SourceModelID  string   `json:"source_model_id,omitempty"`
}

// NewTextMessage creates a simple text-only message.
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: text},
		},
	}
}

// NewUserMessage creates a user message with text content.
func NewUserMessage(text string) Message {
	return NewTextMessage(RoleUser, text)
}

// NewUserMessageWithImage creates a user message with text and an image.
func NewUserMessageWithImage(text, imagePath string) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: ContentBlockText, Text: text},
			{Type: ContentBlockImage, ImageData: imagePath},
		},
	}
}

// NewSystemMessage creates a system message with text content.
func NewSystemMessage(text string) Message {
	return NewTextMessage(RoleSystem, text)
}

// NewAssistantMessage creates an assistant message with the given content blocks.
func NewAssistantMessage(blocks []ContentBlock) Message {
	return Message{
		Role:    RoleAssistant,
		Content: blocks,
	}
}

// NewToolResultMessage creates a tool result message.
func NewToolResultMessage(toolCallID, toolName, text string, isError bool) Message {
	return Message{
		Role: RoleToolResult,
		Content: []ContentBlock{
			{
				Type:       ContentBlockToolResult,
				ToolCallID: toolCallID,
				ToolName:   toolName,
				Text:       text,
				IsError:    isError,
			},
		},
	}
}

// Usage holds token counts for a message or conversation turn.
type Usage struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
	CacheWrite1hTokens  int     `json:"cache_write_1h_tokens,omitempty"`
	ReasoningTokens     int     `json:"reasoning_tokens,omitempty"`
	Cost                float64 `json:"cost,omitempty"`
}

// ImageContent holds image data for content blocks.
type ImageContent struct {
	Data     string `json:"data"`      // Base64-encoded image data
	MimeType string `json:"mime_type"` // e.g., "image/png"
}

// ToolSchema describes a tool that the LLM can call.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// Context is the full conversation context sent to the LLM on a single stream
// invocation.
type Context struct {
	Context      context.Context `json:"-"`
	SystemPrompt string          `json:"system_prompt,omitempty"`
	Messages     []Message       `json:"messages"`
	Tools        []ToolSchema    `json:"tools,omitempty"`
}

// GoContext returns the embedded Go context, or context.Background() if nil.
func (c Context) GoContext() context.Context {
	if c.Context == nil {
		return context.Background()
	}
	return c.Context
}

// ModelPricing holds per-token pricing for a model.
type ModelPricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Model describes a single LLM model with its capabilities, pricing, and
// provider-specific configuration.
type Model struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Api      Api      `json:"api"`
	Provider Provider `json:"provider"`
	BaseURL  string   `json:"base_url,omitempty"`

	Reasoning bool `json:"reasoning,omitempty"`

	InputTypes []string `json:"input_types,omitempty"`

	// IsVisionModel is true when the model supports image inputs.
	IsVisionModel bool `json:"is_vision_model,omitempty"`

	Cost ModelPricing `json:"cost"`

	ContextWindow int `json:"context_window"`
	MaxTokens     int `json:"max_tokens"`

	Headers map[string]string `json:"headers,omitempty"`

	Extra map[string]interface{} `json:"extra,omitempty"`

	VariantID string `json:"variant_id,omitempty"`

	// ThinkingLevelMap maps canonical thinking levels to provider-specific values.
	// TODO: these fields are variant-specific and planned for migration into
	// VariantProfile. They remain on Model during the migration so existing
	// provider code continues to compile. New code should prefer resolving the
	// VariantProfile via schema.ResolveProfile(model) when only profile-level
	// defaults are needed, but config-level overrides still populate this field.
	ThinkingLevelMap ThinkingLevelMap `json:"thinking_level_map,omitempty"`
	ThinkingBudgets  ThinkingBudgets  `json:"thinking_budgets,omitempty"`
	ThinkingFormat   ThinkingFormat   `json:"thinking_format,omitempty"`
	Compat           any              `json:"compat,omitempty"`
}

// StreamOptions configures an LLM streaming request.
type StreamOptions struct {
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	TopK        *float64        `json:"top_k,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Signal      <-chan struct{} `json:"-"`
	APIKey      string          `json:"api_key,omitempty"`

	Transport               Transport     `json:"transport,omitempty"`
	WebsocketConnectTimeout time.Duration `json:"websocket_connect_timeout,omitempty"`

	CacheRetention CacheRetention `json:"cache_retention,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`

	Headers     map[string]string `json:"headers,omitempty"`
	Timeout     time.Duration     `json:"timeout,omitempty"`
	IdleTimeout time.Duration     `json:"idle_timeout,omitempty"`

	MaxRetries    int           `json:"max_retries,omitempty"`
	MaxRetryDelay time.Duration `json:"max_retry_delay,omitempty"`

	ToolChoice string `json:"tool_choice,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`

	OnPayload  func(payload, model any) (any, error)       `json:"-"`
	OnResponse func(status int, headers map[string]string) `json:"-"`

	ServiceTier string `json:"service_tier,omitempty"`

	// Reasoning carries the high-level reasoning level selected by the caller.
	// It is populated by StreamSimple and consumed by protocol builders.
	Reasoning ThinkingLevel `json:"reasoning,omitempty"`
}

// SimpleStreamOptions extends StreamOptions with high-level reasoning controls.
type SimpleStreamOptions struct {
	StreamOptions

	Reasoning       ThinkingLevel    `json:"reasoning,omitempty"`
	ThinkingBudgets *ThinkingBudgets `json:"thinking_budgets,omitempty"`
}
