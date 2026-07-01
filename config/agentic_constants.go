// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

// Agentic provider identifiers. These mirror the constants in
// github.com/pijalu/goa/internal/agentic/provider so Goa's config and wizard can refer
// to them without importing the provider package everywhere.
const (
	AgenticProviderOpenAI     = "openai"
	AgenticProviderAnthropic  = "anthropic"
	AgenticProviderGoogle     = "google"
	AgenticProviderMistral    = "mistral"
	AgenticProviderAWS        = "aws"
	AgenticProviderAzure      = "azure"
	AgenticProviderGitHub     = "github"
	AgenticProviderTogether   = "together"
	AgenticProviderFireworks  = "fireworks"
	AgenticProviderGroq       = "groq"
	AgenticProviderPerplexity = "perplexity"
	AgenticProviderDeepSeek   = "deepseek"
	AgenticProviderOpenRouter = "openrouter"
	AgenticProviderLMStudio   = "lm-studio"
	AgenticProviderOllama     = "ollama"
	AgenticProviderKimi       = "kimi"
	AgenticProviderKimiCode   = "kimi-code"
	AgenticProviderOpenCode   = "opencode"
	AgenticProviderOpenCodeGo = "opencode-go"
	AgenticProviderCustom     = "custom"
)

// Agentic API identifiers. These mirror the constants in
// github.com/pijalu/goa/internal/agentic/provider.
const (
	AgenticAPIOpenAICompletions    = "openai-completions"
	AgenticAPIOpenAIResponses      = "openai-responses"
	AgenticAPIOpenAICodexResponses = "openai-codex-responses"
	AgenticAPIAzureOpenAIResponses = "azure-openai-responses"
	AgenticAPIAnthropicMessages    = "anthropic-messages"
	AgenticAPIGoogleGenerativeAI   = "google-generative-ai"
	AgenticAPIGoogleVertex         = "google-vertex"
	AgenticAPIMistralConversations = "mistral-conversations"
	AgenticAPIBedrockConverse      = "bedrock-converse-stream"
)

// AgenticTransport values mirror provider.Transport.
const (
	AgenticTransportSSE       = "sse"
	AgenticTransportWebSocket = "websocket"
)

// AgenticCacheRetention values mirror provider.CacheRetention.
const (
	AgenticCacheRetentionNone  = "none"
	AgenticCacheRetentionShort = "short"
	AgenticCacheRetentionLong  = "long"
)

// AgenticThinkingLevels mirror provider.ThinkingLevel.
const (
	AgenticThinkingOff     = "off"
	AgenticThinkingMinimal = "minimal"
	AgenticThinkingLow     = "low"
	AgenticThinkingMedium  = "medium"
	AgenticThinkingHigh    = "high"
	AgenticThinkingXHigh   = "xhigh"
	AgenticThinkingMax     = "max"
)

// DefaultThinkingLevelMap maps Goa's canonical thinking levels to token
// budgets. Models that do not define their own map inherit these values.
var DefaultThinkingLevelMap = map[string]int{
	"off":     0,
	"minimal": 1024,
	"low":     2048,
	"medium":  8192,
	"high":    16384,
	"xhigh":   32768,
}

// AgenticCompressionStrategies mirror agentic.CompressionStrategy.
const (
	AgenticCompressionToolElision = "tool_elision"
	AgenticCompressionSelective   = "selective"
	AgenticCompressionSummarize   = "summarize"
	AgenticCompressionHybrid      = "hybrid"
	AgenticCompressionMicro       = "micro"
)

// AgenticSkillExecutionModes mirror agentic.SkillExecutionMode.
const (
	AgenticSkillModeSubAgent = "subagent"
	AgenticSkillModeInline   = "inline"
)

// ValidAgenticProviders returns the set of provider identifiers supported by agentic.
func ValidAgenticProviders() []string {
	return []string{
		AgenticProviderOpenAI,
		AgenticProviderAnthropic,
		AgenticProviderGoogle,
		AgenticProviderMistral,
		AgenticProviderAWS,
		AgenticProviderAzure,
		AgenticProviderGitHub,
		AgenticProviderTogether,
		AgenticProviderFireworks,
		AgenticProviderGroq,
		AgenticProviderPerplexity,
		AgenticProviderDeepSeek,
		AgenticProviderOpenRouter,
		AgenticProviderLMStudio,
		AgenticProviderOllama,
		AgenticProviderKimi,
		AgenticProviderKimiCode,
		AgenticProviderOpenCode,
		AgenticProviderOpenCodeGo,
		AgenticProviderCustom,
	}
}

// ValidAgenticAPIs returns the set of API identifiers supported by agentic.
func ValidAgenticAPIs() []string {
	return []string{
		AgenticAPIOpenAICompletions,
		AgenticAPIOpenAIResponses,
		AgenticAPIOpenAICodexResponses,
		AgenticAPIAzureOpenAIResponses,
		AgenticAPIAnthropicMessages,
		AgenticAPIGoogleGenerativeAI,
		AgenticAPIGoogleVertex,
		AgenticAPIMistralConversations,
		AgenticAPIBedrockConverse,
	}
}

// IsValidAgenticProvider reports whether v is a known agentic provider.
func IsValidAgenticProvider(v string) bool {
	for _, p := range ValidAgenticProviders() {
		if p == v {
			return true
		}
	}
	return v == ""
}

// IsValidAgenticAPI reports whether v is a known agentic API.
func IsValidAgenticAPI(v string) bool {
	for _, a := range ValidAgenticAPIs() {
		if a == v {
			return true
		}
	}
	return v == ""
}
