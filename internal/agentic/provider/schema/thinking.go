// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// ThinkingLevel controls the amount of reasoning an LLM performs.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

// ThinkingLevelMap maps canonical ThinkingLevels to provider-specific values.
type ThinkingLevelMap map[ThinkingLevel]string

// ThinkingBudgets maps ThinkingLevels to per-level token budgets.
type ThinkingBudgets map[ThinkingLevel]int

// ThinkingFormat describes how the native provider represents thinking/reasoning
// content in its response format.
type ThinkingFormat string

const (
	ThinkingFormatNone               ThinkingFormat = "none"
	ThinkingFormatThinkingContent    ThinkingFormat = "thinking_content"
	ThinkingFormatReasoningContent   ThinkingFormat = "reasoning_content"
	ThinkingFormatChunkedReasoning   ThinkingFormat = "chunked_reasoning"
	ThinkingFormatSignatureReasoning ThinkingFormat = "signature_reasoning"
	ThinkingFormatSeparateField      ThinkingFormat = "separate_field"
	ThinkingFormatNoOutput           ThinkingFormat = "no_output"
	ThinkingFormatTextPrefixed       ThinkingFormat = "text_prefixed"

	// Provider-specific thinking formats mapped from the reviewed plan.
	ThinkingFormatOpenAI           ThinkingFormat = "openai"
	ThinkingFormatDeepSeek         ThinkingFormat = "deepseek"
	ThinkingFormatZai              ThinkingFormat = "zai"
	ThinkingFormatTogether         ThinkingFormat = "together"
	ThinkingFormatOpenRouter       ThinkingFormat = "openrouter"
	ThinkingFormatAntLing          ThinkingFormat = "ant-ling"
	ThinkingFormatStringThinking   ThinkingFormat = "string-thinking"
	ThinkingFormatQwen             ThinkingFormat = "qwen"
	ThinkingFormatQwenChatTemplate ThinkingFormat = "qwen-chat-template"
	ThinkingFormatChatTemplate     ThinkingFormat = "chat-template"
	ThinkingFormatChatTemplateArg  ThinkingFormat = "chat-template-arg"
)
