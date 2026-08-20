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

// TotalInputTokens returns the gross input token count the provider charged
// against the model's context window: uncached input plus cache reads and
// cache writes. Provider adapters report InputTokens NET of cache (for cost
// accounting), so the gross figure — the number that determines how full the
// context window actually is — is the sum. This is the ground truth for
// context-window occupancy; never use the net InputTokens for that purpose.
func (u *Usage) TotalInputTokens() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
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
	// NoTools collapses tools for this single request (P7): the wire body
	// omits the tools array and forces tool_choice "none", so the model
	// cannot call tools and must answer text-only. Used for final-step /
	// stop-turn summary requests; the next request restores the full set.
	NoTools bool `json:"-"`
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

// RetryMode selects how a provider route retries model-request failures.
type RetryMode string

const (
	// RetryModeNormal retries only failures classified as eligible (by default
	// the transient set: EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT,
	// TRANSPORT) and only up to a finite MaxRetries budget.
	RetryModeNormal RetryMode = "normal"
	// RetryModeAlways retries every model-request failure without an attempt
	// limit. Success, cancellation, or agent teardown stops the retry loop.
	RetryModeAlways RetryMode = "always"
)

// RetryBackoff is the resolved exponential-backoff schedule for one retry
// policy. Zero values mean "use the package default" for that axis.
type RetryBackoff struct {
	// InitialDelay is the base delay for the first retry (doubles per
	// attempt). Zero falls back to the default 1s.
	InitialDelay time.Duration `json:"initial_delay,omitempty"`
	// MaxDelay caps both the local exponential delay and any accepted
	// provider Retry-After. Zero falls back to the default 30s.
	MaxDelay time.Duration `json:"max_delay,omitempty"`
	// Jitter is the symmetric random multiplier range around one (0.1 = ±10%).
	// Zero falls back to the default 0.25 (legacy fixed +250ms is replaced by
	// symmetric jitter only when a policy is explicitly configured).
	Jitter float64 `json:"jitter,omitempty"`
}

// RetryPolicy is the resolved per-provider model-request retry policy. A nil
// policy means "legacy behavior": normal mode with the StreamOptions
// MaxRetries/MaxRetryDelay scalars and the historical fixed-jitter backoff.
type RetryPolicy struct {
	// Mode selects normal (bounded, code-eligible) or always (unbounded)
	// retry behavior.
	Mode RetryMode `json:"mode,omitempty"`
	// MaxRetries is the finite retry budget for normal mode. Zero falls back
	// to the StreamOptions MaxRetries scalar (or 5).
	MaxRetries int `json:"max_retries,omitempty"`
	// Backoff schedules the delay between attempts.
	Backoff RetryBackoff `json:"backoff,omitempty"`
	// Codes restricts normal-mode retries to the listed failure codes
	// (canonical vocabulary: EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT,
	// TRANSPORT). Empty means the default transient set applies.
	Codes []string `json:"codes,omitempty"`
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

	// Purpose classifies this request for transport-level attribution and
	// per-purpose option locks (P13, CA2/CA3; dsh GenerateOptions.purpose).
	// Empty means PurposeConversation.
	Purpose Purpose `json:"purpose,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`
	// Timeout bounds the connection phase only (dial → first response
	// header). Body reads are guarded by IdleTimeout, never by Timeout, so
	// long-running generations on slow models are not capped.
	Timeout     time.Duration `json:"timeout,omitempty"`
	IdleTimeout time.Duration `json:"idle_timeout,omitempty"`

	MaxRetries    int           `json:"max_retries,omitempty"`
	MaxRetryDelay time.Duration `json:"max_retry_delay,omitempty"`

	// RetryPolicy, when non-nil, replaces the legacy scalar retry behavior
	// (MaxRetries/MaxRetryDelay + fixed-jitter backoff) with a per-provider
	// policy resolved at provider construction: mode (normal/always), finite
	// budget, backoff schedule, and eligible failure codes.
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	ToolChoice string `json:"tool_choice,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`

	OnPayload  func(payload, model any) (any, error)       `json:"-"`
	OnResponse func(status int, headers map[string]string) `json:"-"`

	ServiceTier string `json:"service_tier,omitempty"`

	// CodexAccountID carries the OpenAI ChatGPT account id when the credential
	// is an OAuth subscription token (vs a plain API key). Its presence selects
	// the Codex subscription transport: base URL https://chatgpt.com/backend-api
	// and the chatgpt-account-id/originator/OpenAI-Beta identity headers.
	CodexAccountID string `json:"codex_account_id,omitempty"`

	// Reasoning carries the high-level reasoning level selected by the caller.
	// It is populated by StreamSimple and consumed by protocol builders.
	Reasoning ThinkingLevel `json:"reasoning,omitempty"`
}

// Purpose classifies the intent of a streaming request for transport-level
// attribution and per-purpose option locks (P13, CA2/CA3). It mirrors dsh's
// GenerateOptions.purpose (packages/llm/llm-deepseek/README.md §App
// attribution).
type Purpose string

const (
	// PurposeConversation is the default for ordinary user turns.
	PurposeConversation Purpose = "conversation"

	// PurposeCompaction marks the auxiliary summarization call that shrinks
	// history. DeepSeek-compat routes tag it with x-goa-compact: 1 so hosts
	// can separate compaction traffic from conversation requests.
	PurposeCompaction Purpose = "compaction"

	// PurposeSessionTitle marks a bounded title-generation call. It forces
	// thinking off (mirrors the DS-thinking lock), reserving the model's
	// output for visible title text without changing conversation or
	// compaction defaults.
	PurposeSessionTitle Purpose = "session-title"
)

// SimpleStreamOptions extends StreamOptions with high-level reasoning controls.
type SimpleStreamOptions struct {
	StreamOptions

	Reasoning       ThinkingLevel    `json:"reasoning,omitempty"`
	ThinkingBudgets *ThinkingBudgets `json:"thinking_budgets,omitempty"`
}
