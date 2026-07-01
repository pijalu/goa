// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

// ---------------------------------------------------------------------------
// OpenAICompletionsCompat
// ---------------------------------------------------------------------------

// OpenAICompletionsCompat carries per-provider compatibility flags for
// OpenAI-compatible completions APIs. Fields use *bool and *string so that
// nil distinguishes "not explicitly set" from "set to zero".
type OpenAICompletionsCompat struct {
	SupportsStore                               *bool   `json:"supportsStore,omitempty"`
	SupportsDeveloperRole                       *bool   `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort                     *bool   `json:"supportsReasoningEffort,omitempty"`
	SupportsUsageInStreaming                    *bool   `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                              *string `json:"maxTokensField,omitempty"`
	RequiresToolResultName                      *bool   `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult            *bool   `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText                      *bool   `json:"requiresThinkingAsText,omitempty"`
	RequiresReasoningContentOnAssistantMessages *bool   `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	ThinkingFormat                              *string `json:"thinkingFormat,omitempty"`
	ZaiToolStream                               *bool   `json:"zaiToolStream,omitempty"`
	SupportsStrictMode                          *bool   `json:"supportsStrictMode,omitempty"`
	CacheControlFormat                          *string `json:"cacheControlFormat,omitempty"`
	SendSessionAffinityHeaders                  *bool   `json:"sendSessionAffinityHeaders,omitempty"`
	SupportsLongCacheRetention                  *bool   `json:"supportsLongCacheRetention,omitempty"`
	// ToolResultAsUser formats tool results as user messages with XML-style
	// markers instead of role: "tool".  Some models (e.g. Gemma via LM Studio,
	// Qwen) don't properly handle role: "tool" and fail to associate results
	// with their calls, causing repeated tool-call loops.
	ToolResultAsUser *bool `json:"toolResultAsUser,omitempty"`
}

// ToBool returns the value of a *bool field, or a fallback if nil.
func ToBool(v *bool, fallback bool) bool {
	if v != nil {
		return *v
	}
	return fallback
}

// ToString returns the value of a *string field, or a fallback if nil.
func ToString(v *string, fallback string) string {
	if v != nil {
		return *v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// OpenAIResponsesCompat
// ---------------------------------------------------------------------------

// OpenAIResponsesCompat carries compatibility flags for OpenAI Responses API.
type OpenAIResponsesCompat struct {
	SendSessionIDHeader        *bool `json:"sendSessionIdHeader,omitempty"`
	SupportsLongCacheRetention *bool `json:"supportsLongCacheRetention,omitempty"`
}

// ---------------------------------------------------------------------------
// AnthropicMessagesCompat
// ---------------------------------------------------------------------------

// AnthropicMessagesCompat carries per-provider compatibility flags for
// Anthropic Messages-compatible APIs.
type AnthropicMessagesCompat struct {
	SupportsEagerToolInputStreaming *bool    `json:"supportsEagerToolInputStreaming,omitempty"`
	SupportsLongCacheRetention      *bool    `json:"supportsLongCacheRetention,omitempty"`
	SendSessionAffinityHeaders      *bool    `json:"sendSessionAffinityHeaders,omitempty"`
	SupportsCacheControlOnTools     *bool    `json:"supportsCacheControlOnTools,omitempty"`
	SupportsTemperature             *bool    `json:"supportsTemperature,omitempty"`
	RequiresAdaptiveThinking        *bool    `json:"requiresAdaptiveThinking,omitempty"`
	SupportsThinkingOnTools         *bool    `json:"supportsThinkingOnTools,omitempty"`
	ThinkingBudgetMultiplier        *float64 `json:"thinkingBudgetMultiplier,omitempty"`
}
