// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// ProfileMatch selects which models a variant profile applies to.
type ProfileMatch struct {
	API       string `json:"api,omitempty"`
	Provider  string `json:"provider,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	ModelID   string `json:"model_id,omitempty"`
	VariantID string `json:"variant_id,omitempty"`
}

// CompatFlags holds provider-specific compatibility flags.
type CompatFlags struct {
	SupportsStore                               *bool          `json:"supports_store,omitempty"`
	MaxTokensField                              string         `json:"max_tokens_field,omitempty"`
	ThinkingFormat                              string         `json:"thinking_format,omitempty"`
	RequiresReasoningContentOnAssistantMessages bool           `json:"requires_reasoning_content_on_assistant_messages,omitempty"`
	SystemAsInstructions                        bool           `json:"system_as_instructions,omitempty"`
	SupportsPromptCache                         bool           `json:"supports_prompt_cache,omitempty"`
	ReasoningKey                                string         `json:"reasoning_key,omitempty"`
	ThinkingExtraBody                           map[string]any `json:"thinking_extra_body,omitempty"`
	StreamIncludesUsage                         bool           `json:"stream_includes_usage,omitempty"`
	RequiresEmptyToolArguments                  bool           `json:"requires_empty_tool_arguments,omitempty"`
	DropNullContent                             bool           `json:"drop_null_content,omitempty"`
	ImageDetailSupported                        bool           `json:"image_detail_supported,omitempty"`
	ImageURLScheme                              string         `json:"image_url_scheme,omitempty"`
}

// Defaults holds per-variant default request parameters.
type Defaults struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	TopK            *float64        `json:"top_k,omitempty"`
	MaxTokens       *int            `json:"max_tokens,omitempty"`
	Thinking        string          `json:"thinking,omitempty"`
	ThinkingLevelMap ThinkingLevelMap `json:"thinking_level_map,omitempty"`
	ThinkingBudgets ThinkingBudgets `json:"thinking_budgets,omitempty"`
}

// ErrorRules describes how to classify and retry provider errors.
type ErrorRules struct {
	RetryableStatuses       []int    `json:"retryable_statuses,omitempty"`
	ContextOverflowPatterns []string `json:"context_overflow_patterns,omitempty"`
	NonOverflowPatterns     []string `json:"non_overflow_patterns,omitempty"`
	RetryAfterHeader        string   `json:"retry_after_header,omitempty"`
	RetryAfterMsHeader      string   `json:"retry_after_ms_header,omitempty"`
	RetryOnceStatuses       []int    `json:"retry_once_statuses,omitempty"`
}

// VariantProfile is the canonical configuration for a provider variant.
type VariantProfile struct {
	ID            string            `json:"id,omitempty"`
	Match         ProfileMatch      `json:"match"`
	Defaults      Defaults          `json:"defaults,omitempty"`
	Compat        CompatFlags       `json:"compat,omitempty"`
	Auth          AuthConfig        `json:"auth,omitempty"`
	Headers       []HeaderRule      `json:"headers,omitempty"`
	CachePolicy   CachePolicy       `json:"cache_policy,omitempty"`
	ToolCompat    ToolCompat        `json:"tool_compat,omitempty"`
	SdkKey        SdkKeyConfig      `json:"sdk_key,omitempty"`
	ErrorRules    ErrorRules        `json:"error_rules,omitempty"`
	FieldMappings map[string]string `json:"field_mappings,omitempty"`
}

// IsEmpty reports whether the profile has no meaningful configuration.
func (p VariantProfile) IsEmpty() bool {
	return p.Match.API == "" && p.Match.Provider == ""
}
