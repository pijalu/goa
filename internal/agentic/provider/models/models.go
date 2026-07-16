// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import "github.com/pijalu/goa/internal/agentic/provider"

// modelDefs is the curated set of ~30 models covering all target API types.
var modelDefs = []provider.Model{
	// ── OpenAI ──
	{
		ID: "gpt-4o", Name: "GPT-4o", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 16384, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000025, Output: 0.00001},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "gpt-4o-mini", Name: "GPT-4o Mini", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 16384, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.00000015, Output: 0.0000006},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "o1", Name: "o1", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000015, Output: 0.00006},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "off", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
	},
	{
		ID: "o3-mini", Name: "o3 Mini", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 100000, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.0000011, Output: 0.0000044},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "off", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
	},

	// ── Anthropic ──
	{
		ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", Api: provider.ApiAnthropicMessages, Provider: provider.ProviderAnthropic,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000003, Output: 0.000015, CacheRead: 0.0000003, CacheWrite: 0.00000375},
		ThinkingFormat: provider.ThinkingFormatThinkingContent,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "none", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
	},
	{
		ID: "claude-opus-4-20250514", Name: "Claude Opus 4", Api: provider.ApiAnthropicMessages, Provider: provider.ProviderAnthropic,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000015, Output: 0.000075, CacheRead: 0.0000015, CacheWrite: 0.00001875},
		ThinkingFormat: provider.ThinkingFormatThinkingContent,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "none", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
	},
	{
		ID: "claude-haiku-3-5-20241022", Name: "Claude Haiku 3.5", Api: provider.ApiAnthropicMessages, Provider: provider.ProviderAnthropic,
		Reasoning: false, ContextWindow: 200000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000008, Output: 0.000004, CacheRead: 0.00000008, CacheWrite: 0.000001},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Google ──
	{
		ID: "gemini-", Name: "Gemini (generic)", Api: provider.ApiGoogleGenerativeAI, Provider: provider.ProviderGoogle,
		Reasoning: false, ContextWindow: 1048576, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000001, Output: 0.0000004},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Api: provider.ApiGoogleGenerativeAI, Provider: provider.ProviderGoogle,
		Reasoning: true, ContextWindow: 1048576, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.00000125, Output: 0.00001},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},
	{
		ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", Api: provider.ApiGoogleGenerativeAI, Provider: provider.ProviderGoogle,
		Reasoning: false, ContextWindow: 1048576, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000001, Output: 0.0000004},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Mistral ──
	{
		ID: "mistral-large-2", Name: "Mistral Large 2", Api: provider.ApiMistralConversations, Provider: provider.ProviderMistral,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000002, Output: 0.000006},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "mistral-small-3", Name: "Mistral Small 3", Api: provider.ApiMistralConversations, Provider: provider.ProviderMistral,
		Reasoning: false, ContextWindow: 32000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000001, Output: 0.000003},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── DeepSeek ──
	{
		ID: "deepseek-", Name: "DeepSeek (generic)", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000027, Output: 0.0000011},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff:    "off",
			provider.ThinkingLow:    "low",
			provider.ThinkingMedium: "medium",
			provider.ThinkingHigh:   "high",
			provider.ThinkingXHigh:  "max",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},
	{
		ID: "deepseek-chat", Name: "DeepSeek Chat (V3)", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000027, Output: 0.0000011},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff:    "off",
			provider.ThinkingLow:    "low",
			provider.ThinkingMedium: "medium",
			provider.ThinkingHigh:   "high",
			provider.ThinkingXHigh:  "max",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},
	{
		ID: "deepseek-reasoner", Name: "DeepSeek Reasoner (R1)", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000055, Output: 0.00000219},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff:    "off",
			provider.ThinkingLow:    "low",
			provider.ThinkingMedium: "medium",
			provider.ThinkingHigh:   "high",
			provider.ThinkingXHigh:  "max",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},
	{
		ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000014, Output: 0.00000028, CacheRead: 0.000000028, CacheWrite: 0},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff:    "off",
			provider.ThinkingLow:    "low",
			provider.ThinkingMedium: "medium",
			provider.ThinkingHigh:   "high",
			provider.ThinkingXHigh:  "max",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},
	{
		ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000000435, Output: 0.00000087, CacheRead: 0.00000003625, CacheWrite: 0},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff:    "off",
			provider.ThinkingLow:    "low",
			provider.ThinkingMedium: "medium",
			provider.ThinkingHigh:   "high",
			provider.ThinkingXHigh:  "max",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},

	// ── Together / Fireworks / Groq ──
	{
		ID: "llama-3.3-70b", Name: "Llama 3.3 70B", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderTogether,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000059, Output: 0.00000079},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "llama-3.1-8b", Name: "Llama 3.1 8B", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderFireworks,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.0000001, Output: 0.0000001},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B (Groq)", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderGroq,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000059, Output: 0.00000079},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Perplexity ──
	{
		ID: "sonar-pro", Name: "Sonar Pro", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderPerplexity,
		Reasoning: false, ContextWindow: 127000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000003, Output: 0.000015},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Kimi / Moonshot ──
	{
		ID: "kimi-k2.6", Name: "Kimi K2.6", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderKimi,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000002, Output: 0.000008},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},

	// ── Kimi Code ──
	{
		ID: "kimi-for-coding", Name: "Kimi Code Coding", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderKimiCode,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 16384, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000002, Output: 0.000008},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},
	{
		// Kimi K3 (model ID "k3"). Reasoning opt-in is reasoning_effort:max
		// (k3 is max-only today; low/high come later). The kimi-code profile
		// sends reasoning_effort:max by default, so thinking works zero-config.
		//
		// ContextWindow: k3 supports up to 1M (1048576) but ONLY on the
		// Allegretto+ plan; Moderato caps at 256k and lower tiers have no k3
		// access (server returns 401). We ship the max so Allegretto users get
		// full context; Moderato users must set context_window: 262144 in YAML
		// to avoid over-budget compression/ceiling math against a 256k server.
		//
		// Cost is intentionally zero: Kimi Code is membership/quota-based (no
		// published per-token price), so there is no honest rate to show.
		ID: "k3", Name: "Kimi K3", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderKimiCode,
		Reasoning: true, ContextWindow: 1048576, MaxTokens: 16384, InputTypes: []string{"text"},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},

	// ── Qwen ──
	{
		ID: "qwen/qwen3.5-9b", Name: "Qwen 3.5 9B", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderCustom,
		Reasoning: false, ContextWindow: 32768, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "qwen/qwen3.5-32b", Name: "Qwen 3.5 32B", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderCustom,
		Reasoning: false, ContextWindow: 65536, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "qwen/qwen3.5-72b", Name: "Qwen 3.5 72B", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderCustom,
		Reasoning: false, ContextWindow: 131072, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── OpenRouter ──
	{
		ID: "openrouter-auto", Name: "OpenRouter Auto", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenRouter,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Local providers ──
	{
		ID: "lm-studio-default", Name: "LM Studio Default", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderLMStudio,
		Reasoning: false, ContextWindow: 4096, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "ollama-default", Name: "Ollama Default", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOllama,
		Reasoning: false, ContextWindow: 4096, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── AWS Bedrock ──
	{
		ID: "claude-sonnet-4-bedrock", Name: "Claude Sonnet 4 (Bedrock)", Api: provider.ApiBedrockConverse, Provider: provider.ProviderAWS,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000003, Output: 0.000015},
		ThinkingFormat: provider.ThinkingFormatThinkingContent,
	},

	// ── Azure ──
	{
		ID: "gpt-4o-azure", Name: "GPT-4o (Azure)", Api: provider.ApiAzureOpenAIResponses, Provider: provider.ProviderAzure,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 16384, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000025, Output: 0.00001},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── xAI ──
	{
		ID: "grok-3", Name: "Grok 3", Api: provider.ApiOpenAICompletions, Provider: "xai",
		Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000003, Output: 0.000015},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},

	// ── GitHub Copilot ──
	{
		ID: "copilot-gpt-4o", Name: "Copilot GPT-4o", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderGitHub,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 4096, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── GPT-5 family ──
	{
		ID: "gpt-5.5", Name: "GPT-5.5", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: false, ContextWindow: 256000, MaxTokens: 16384, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000001, Output: 0.000004},
		ThinkingFormat: provider.ThinkingFormatNone,
	},
	{
		ID: "gpt-5-pro", Name: "GPT-5 Pro", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenAI,
		Reasoning: true, ContextWindow: 256000, MaxTokens: 32768, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000005, Output: 0.00002},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},

	// ── Claude 4+ ──
	{
		ID: "claude-opus-4-7", Name: "Claude Opus 4.7", Api: provider.ApiAnthropicMessages, Provider: provider.ProviderAnthropic,
		Reasoning: true, ContextWindow: 200000, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.000015, Output: 0.000075, CacheRead: 0.0000015, CacheWrite: 0.00001875},
		ThinkingFormat: provider.ThinkingFormatThinkingContent,
	},

	// ── Gemini 3 ──
	{
		ID: "gemini-3.1-pro", Name: "Gemini 3.1 Pro", Api: provider.ApiGoogleGenerativeAI, Provider: provider.ProviderGoogle,
		Reasoning: true, ContextWindow: 1048576, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.00000125, Output: 0.00001},
		ThinkingFormat: provider.ThinkingFormatReasoningContent,
	},
	{
		ID: "gemma-4-e4b", Name: "Gemma 4 E4B", Api: provider.ApiGoogleGenerativeAI, Provider: provider.ProviderGoogle,
		Reasoning: false, ContextWindow: 131072, MaxTokens: 8192, InputTypes: []string{"text", "image"},
		Cost:           provider.ModelPricing{Input: 0.0000001, Output: 0.0000002},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── Mistral ──
	{
		ID: "mistral-small-2603", Name: "Mistral Small 2603", Api: provider.ApiMistralConversations, Provider: provider.ProviderMistral,
		Reasoning: false, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.000001, Output: 0.000003},
		ThinkingFormat: provider.ThinkingFormatNone,
	},

	// ── DeepSeek ──
	{
		ID: "deepseek-v3", Name: "DeepSeek V3", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderDeepSeek,
		Reasoning: true, ContextWindow: 128000, MaxTokens: 8192, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000027, Output: 0.0000011},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "off", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},

	// ── OpenCode Zen ──
	{
		ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenCode,
		Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000014, Output: 0.00000028, CacheRead: 0.0000028, CacheWrite: 0},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "off", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},

	// ── OpenCode Zen Go ──
	{
		ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Api: provider.ApiOpenAICompletions, Provider: provider.ProviderOpenCodeGo,
		Reasoning: true, ContextWindow: 1000000, MaxTokens: 384000, InputTypes: []string{"text"},
		Cost:           provider.ModelPricing{Input: 0.00000014, Output: 0.00000028, CacheRead: 0.0000028, CacheWrite: 0},
		ThinkingFormat: provider.ThinkingFormatChunkedReasoning,
		ThinkingLevelMap: provider.ThinkingLevelMap{
			provider.ThinkingOff: "off", provider.ThinkingLow: "low",
			provider.ThinkingMedium: "medium", provider.ThinkingHigh: "high",
		},
		Compat: provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		},
	},
}

// BoolPtr returns a pointer to a bool, for use in compat structs.
func BoolPtr(v bool) *bool { return &v }
