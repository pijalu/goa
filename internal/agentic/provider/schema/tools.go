// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// ToolCallIDRules describes how tool call IDs are normalized for a provider.
type ToolCallIDRules struct {
	MaxLength     int    `json:"max_length,omitempty"`
	Alphabet      string `json:"alphabet,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	Separator     string `json:"separator,omitempty"`
	ForeignPrefix string `json:"foreign_prefix,omitempty"`
	HashBased     bool   `json:"hash_based,omitempty"`
}

// SchemaSanitizer names a JSON-schema sanitization strategy.
type SchemaSanitizer string

const (
	SchemaSanitizerOpenAI   SchemaSanitizer = "openai"
	SchemaSanitizerMoonshot SchemaSanitizer = "moonshot"
	SchemaSanitizerGemini   SchemaSanitizer = "gemini"
	SchemaSanitizerNone     SchemaSanitizer = "none"
)

// ToolCompat holds provider-specific tool handling flags.
type ToolCompat struct {
	ToolCallIDRules                  ToolCallIDRules `json:"tool_call_id_rules,omitempty"`
	SchemaSanitizer                  SchemaSanitizer `json:"schema_sanitizer,omitempty"`
	ToolResultAsUser                 bool            `json:"tool_result_as_user,omitempty"`
	RequiresToolResultName           bool            `json:"requires_tool_result_name,omitempty"`
	RequiresAssistantAfterToolResult bool            `json:"requires_assistant_after_tool_result,omitempty"`
	ToolStreaming                    *bool           `json:"tool_streaming,omitempty"`
	BuiltinFunctionPrefix            string          `json:"builtin_function_prefix,omitempty"`
	NormalizeNullDescriptions        bool            `json:"normalize_null_descriptions,omitempty"`
	SupportsParallelToolCalls        bool            `json:"supports_parallel_tool_calls,omitempty"`
}
