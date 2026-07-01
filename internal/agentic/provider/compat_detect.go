// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import "strings"

// ---------------------------------------------------------------------------
// Provider fingerprint helpers
// ---------------------------------------------------------------------------

type providerFingerprint struct {
	isZai          bool
	isTogether     bool
	isMoonshot     bool
	isOpenRouter   bool
	isCloudflareWA bool
	isCloudflareAG bool
	isNvidia       bool
	isAntLing      bool
	isGrok         bool
	isDeepSeek     bool
	isCerebras     bool
	isChutes       bool
	isOpenCode     bool
	isLMStudio     bool
	isOllama       bool
}

func fingerprintProvider(providerName Provider, baseURL string) providerFingerprint {
	url := strings.ToLower(baseURL)
	p := strings.ToLower(string(providerName))

	return providerFingerprint{
		isZai:          matchesProviderOrURL(p, url, "zai", "zai-coding-cn", "api.z.ai", "open.bigmodel.cn"),
		isTogether:     matchesProviderOrURL(p, url, "together", "api.together.ai", "api.together.xyz"),
		isMoonshot:     matchesProviderOrURL(p, url, "moonshotai", "moonshotai-cn", "api.moonshot."),
		isOpenRouter:   matchesProviderOrURL(p, url, "openrouter", "openrouter.ai"),
		isCloudflareWA: matchesProviderOrURL(p, url, "cloudflare-workers-ai", "api.cloudflare.com"),
		isCloudflareAG: matchesProviderOrURL(p, url, "cloudflare-ai-gateway", "gateway.ai.cloudflare.com"),
		isNvidia:       matchesProviderOrURL(p, url, "nvidia", "integrate.api.nvidia.com"),
		isAntLing:      matchesProviderOrURL(p, url, "ant-ling", "api.ant-ling.com"),
		isGrok:         matchesProviderOrURL(p, url, "xai", "api.x.ai"),
		isDeepSeek:     matchesProviderOrURL(p, url, "deepseek", "deepseek.com"),
		isCerebras:     matchesProviderOrURL(p, url, "cerebras", "cerebras.ai"),
		isChutes:       strings.Contains(url, "chutes.ai"),
		isOpenCode:     matchesProviderOrURL(p, url, "opencode", "opencode.ai"),
		isLMStudio:     matchesProviderOrURL(p, url, "lm-studio", "lmstudio", "localhost:1234"),
		isOllama:       matchesProviderOrURL(p, url, "ollama", "localhost:11434"),
	}
}

// matchesProviderOrURL returns true if providerName matches any of the given
// name patterns, or if the baseURL contains any of the given URL substrings.
func matchesProviderOrURL(providerName, baseURL string, patterns ...string) bool {
	for _, pat := range patterns {
		if providerName == pat || strings.Contains(baseURL, pat) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// OpenAI compat detection
// ---------------------------------------------------------------------------

// DetectOpenAICompat auto-detects an OpenAICompletionsCompat from a model.
// All returned fields are non-nil (concrete detected values).
func DetectOpenAICompat(model Model) OpenAICompletionsCompat {
	fp := fingerprintProvider(model.Provider, model.BaseURL)

	cacheControlFormat := ""
	if fp.isOpenRouter || fp.isLMStudio || fp.isOllama {
		cacheControlFormat = "anthropic"
	}

	maxTokensField := "max_completion_tokens"
	if fp.useMaxTokens() {
		maxTokensField = "max_tokens"
	}

	return OpenAICompletionsCompat{
		SupportsStore:                               boolPtr(!fp.isNonStandard()),
		SupportsDeveloperRole:                       boolPtr(fp.supportsDeveloperRole()),
		SupportsReasoningEffort:                     boolPtr(fp.supportsReasoningEffort()),
		SupportsUsageInStreaming:                    boolPtr(true),
		MaxTokensField:                              strPtr(maxTokensField),
		RequiresToolResultName:                      boolPtr(false),
		RequiresAssistantAfterToolResult:            boolPtr(false),
		RequiresThinkingAsText:                      boolPtr(false),
		RequiresReasoningContentOnAssistantMessages: boolPtr(fp.isDeepSeek),
		ThinkingFormat:                              strPtr(fp.detectThinkingFormat()),
		ZaiToolStream:                               boolPtr(false),
		SupportsStrictMode:                          boolPtr(!fp.isMoonshot && !fp.isTogether && !fp.isCloudflareAG && !fp.isNvidia),
		CacheControlFormat:                          strPtr(cacheControlFormat),
		SendSessionAffinityHeaders:                  boolPtr(false),
		SupportsLongCacheRetention:                  boolPtr(fp.supportsCacheRetention()),
		ToolResultAsUser:                            boolPtr(fp.needsToolResultAsUser(model.ID)),
	}
}

func (fp providerFingerprint) needsToolResultAsUser(modelID string) bool {
	m := strings.ToLower(modelID)
	if strings.Contains(m, "gemma") || strings.Contains(m, "qwen") {
		return true
	}
	// Local servers often host Gemma/Qwen-style models that also prefer
	// tool results formatted as user messages.
	if fp.isLMStudio || fp.isOllama {
		return true
	}
	return false
}

func (fp providerFingerprint) isNonStandard() bool {
	return fp.isNvidia || fp.isCerebras || fp.isGrok || fp.isTogether ||
		fp.isChutes || fp.isDeepSeek || fp.isZai || fp.isMoonshot ||
		fp.isOpenCode || fp.isCloudflareWA || fp.isCloudflareAG || fp.isAntLing
}

func (fp providerFingerprint) useMaxTokens() bool {
	return fp.isChutes || fp.isMoonshot || fp.isCloudflareAG || fp.isTogether || fp.isNvidia || fp.isAntLing
}

func (fp providerFingerprint) detectThinkingFormat() string {
	switch {
	case fp.isDeepSeek:
		return "deepseek"
	case fp.isZai:
		return "zai"
	case fp.isTogether:
		return "together"
	case fp.isAntLing:
		return "ant-ling"
	case fp.isOpenRouter:
		return "openrouter"
	default:
		return "openai"
	}
}

func (fp providerFingerprint) supportsDeveloperRole() bool {
	// Local providers and several non-standard endpoints don't support the
	// newer "developer" role; stick with "system" for maximum compatibility.
	if fp.isLMStudio || fp.isOllama || fp.isNonStandard() {
		return false
	}
	return true
}

func (fp providerFingerprint) supportsReasoningEffort() bool {
	return !fp.isGrok && !fp.isZai && !fp.isMoonshot && !fp.isTogether &&
		!fp.isCloudflareAG && !fp.isNvidia && !fp.isAntLing
}

func (fp providerFingerprint) supportsCacheRetention() bool {
	return !fp.isTogether && !fp.isCloudflareWA && !fp.isCloudflareAG && !fp.isNvidia && !fp.isAntLing
}

// ResolveOpenAICompat merges explicit model compat flags with auto-detected
// values. Explicit fields override detection when non-nil.
func ResolveOpenAICompat(model Model) OpenAICompletionsCompat {
	detected := DetectOpenAICompat(model)
	explicit, ok := model.Compat.(OpenAICompletionsCompat)
	if !ok {
		return detected
	}
	return mergeOpenAICompat(detected, explicit)
}

// mergeOpenAICompat merges two compat structs. For each field, if the explicit
// value is non-nil, it wins; otherwise the detected value is used.
func mergeOpenAICompat(detected, explicit OpenAICompletionsCompat) OpenAICompletionsCompat {
	return OpenAICompletionsCompat{
		SupportsStore:                               mergeBool(detected.SupportsStore, explicit.SupportsStore),
		SupportsDeveloperRole:                       mergeBool(detected.SupportsDeveloperRole, explicit.SupportsDeveloperRole),
		SupportsReasoningEffort:                     mergeBool(detected.SupportsReasoningEffort, explicit.SupportsReasoningEffort),
		SupportsUsageInStreaming:                    mergeBool(detected.SupportsUsageInStreaming, explicit.SupportsUsageInStreaming),
		MaxTokensField:                              mergeStr(detected.MaxTokensField, explicit.MaxTokensField),
		RequiresToolResultName:                      mergeBool(detected.RequiresToolResultName, explicit.RequiresToolResultName),
		RequiresAssistantAfterToolResult:            mergeBool(detected.RequiresAssistantAfterToolResult, explicit.RequiresAssistantAfterToolResult),
		RequiresThinkingAsText:                      mergeBool(detected.RequiresThinkingAsText, explicit.RequiresThinkingAsText),
		RequiresReasoningContentOnAssistantMessages: mergeBool(detected.RequiresReasoningContentOnAssistantMessages, explicit.RequiresReasoningContentOnAssistantMessages),
		ThinkingFormat:                              mergeStr(detected.ThinkingFormat, explicit.ThinkingFormat),
		ZaiToolStream:                               mergeBool(detected.ZaiToolStream, explicit.ZaiToolStream),
		SupportsStrictMode:                          mergeBool(detected.SupportsStrictMode, explicit.SupportsStrictMode),
		CacheControlFormat:                          mergeStr(detected.CacheControlFormat, explicit.CacheControlFormat),
		SendSessionAffinityHeaders:                  mergeBool(detected.SendSessionAffinityHeaders, explicit.SendSessionAffinityHeaders),
		SupportsLongCacheRetention:                  mergeBool(detected.SupportsLongCacheRetention, explicit.SupportsLongCacheRetention),
		ToolResultAsUser:                            mergeBool(detected.ToolResultAsUser, explicit.ToolResultAsUser),
	}
}

// ---------------------------------------------------------------------------
// Anthropic compat detection
// ---------------------------------------------------------------------------

// DetectAnthropicCompat auto-detects an AnthropicMessagesCompat.
// All returned fields are non-nil.
func DetectAnthropicCompat(providerName Provider, baseURL string) AnthropicMessagesCompat {
	url := strings.ToLower(baseURL)
	p := strings.ToLower(string(providerName))

	isFireworks := p == "fireworks" || strings.Contains(url, "fireworks.ai")
	isTogether := p == "together" || strings.Contains(url, "api.together.ai") || strings.Contains(url, "api.together.xyz")

	return AnthropicMessagesCompat{
		SupportsEagerToolInputStreaming: boolPtr(!isFireworks && !isTogether),
		SupportsLongCacheRetention:      boolPtr(!isFireworks),
		SendSessionAffinityHeaders:      boolPtr(isFireworks),
		SupportsCacheControlOnTools:     boolPtr(!isFireworks),
		SupportsTemperature:             boolPtr(true),
		RequiresAdaptiveThinking:        boolPtr(false),
		SupportsThinkingOnTools:         boolPtr(false),
		ThinkingBudgetMultiplier:        f64Ptr(1.0),
	}
}

// ResolveAnthropicCompat merges explicit model compat with auto-detected values.
func ResolveAnthropicCompat(model Model) AnthropicMessagesCompat {
	detected := DetectAnthropicCompat(model.Provider, model.BaseURL)
	explicit, ok := model.Compat.(AnthropicMessagesCompat)
	if !ok {
		return detected
	}
	return mergeAnthropicCompat(detected, explicit)
}

func mergeAnthropicCompat(detected, explicit AnthropicMessagesCompat) AnthropicMessagesCompat {
	return AnthropicMessagesCompat{
		SupportsEagerToolInputStreaming: mergeBool(detected.SupportsEagerToolInputStreaming, explicit.SupportsEagerToolInputStreaming),
		SupportsLongCacheRetention:      mergeBool(detected.SupportsLongCacheRetention, explicit.SupportsLongCacheRetention),
		SendSessionAffinityHeaders:      mergeBool(detected.SendSessionAffinityHeaders, explicit.SendSessionAffinityHeaders),
		SupportsCacheControlOnTools:     mergeBool(detected.SupportsCacheControlOnTools, explicit.SupportsCacheControlOnTools),
		SupportsTemperature:             mergeBool(detected.SupportsTemperature, explicit.SupportsTemperature),
		RequiresAdaptiveThinking:        mergeBool(detected.RequiresAdaptiveThinking, explicit.RequiresAdaptiveThinking),
		SupportsThinkingOnTools:         mergeBool(detected.SupportsThinkingOnTools, explicit.SupportsThinkingOnTools),
		ThinkingBudgetMultiplier:        mergeF64(detected.ThinkingBudgetMultiplier, explicit.ThinkingBudgetMultiplier),
	}
}

// ---------------------------------------------------------------------------
// ResolveCompat — generic dispatch
// ---------------------------------------------------------------------------

// ResolveCompat returns the resolved compat configuration for a model,
// dispatching on the model's API type.
func ResolveCompat(model Model) any {
	switch model.Api {
	case ApiOpenAICompletions, ApiOpenAIResponses, ApiAzureOpenAIResponses, ApiOpenAICodexResponses:
		return ResolveOpenAICompat(model)
	case ApiAnthropicMessages:
		return ResolveAnthropicCompat(model)
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Merge helpers
// ---------------------------------------------------------------------------

// mergeBool returns explicit if non-nil, otherwise detected.
func mergeBool(detected, explicit *bool) *bool {
	if explicit != nil {
		return explicit
	}
	return detected
}

// mergeStr returns explicit if non-nil, otherwise detected.
func mergeStr(detected, explicit *string) *string {
	if explicit != nil {
		return explicit
	}
	return detected
}

// mergeF64 returns explicit if non-nil, otherwise detected.
func mergeF64(detected, explicit *float64) *float64 {
	if explicit != nil {
		return explicit
	}
	return detected
}

// ---------------------------------------------------------------------------
// Pointer helpers
// ---------------------------------------------------------------------------

func boolPtr(v bool) *bool { return &v }

// BoolPtr returns a pointer to a bool. Exported for use by model definitions.
func BoolPtr(v bool) *bool      { return &v }
func strPtr(v string) *string   { return &v }
func f64Ptr(v float64) *float64 { return &v }
