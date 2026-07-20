// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"strings"
)

// GetEnvAPIKey returns the API key for the given provider by checking
// well-known environment variables providers.
//
// Returns empty string if no known environment variable is set, or if the
// provider doesn't need an API key (e.g., local providers like LM Studio).
func GetEnvAPIKey(provider Provider) string {
	vars := envVarsForProvider(provider)
	for _, v := range vars {
		if val := os.Getenv(v); val != "" {
			return val
		}
	}
	return ""
}

// providerEnvVars maps known providers to their env var names (priority order).
var providerEnvVars = map[Provider][]string{
	ProviderAnthropic:  {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
	ProviderOpenAI:     {"OPENAI_API_KEY"},
	ProviderGoogle:     {"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_API_KEY"},
	ProviderMistral:    {"MISTRAL_API_KEY"},
	ProviderAWS:        {"AWS_ACCESS_KEY_ID"},
	ProviderAzure:      {"AZURE_OPENAI_API_KEY", "AZURE_API_KEY"},
	ProviderGitHub:     {"COPILOT_GITHUB_TOKEN", "GITHUB_TOKEN"},
	ProviderTogether:   {"TOGETHER_API_KEY"},
	ProviderFireworks:  {"FIREWORKS_API_KEY"},
	ProviderGroq:       {"GROQ_API_KEY"},
	ProviderPerplexity: {"PERPLEXITY_API_KEY"},
	ProviderDeepSeek:   {"DEEPSEEK_API_KEY"},
	ProviderOpenRouter: {"OPENROUTER_API_KEY"},
	ProviderOpenCode:   {"OPENCODE_API_KEY"},
	ProviderOpenCodeGo: {"OPENCODE_API_KEY"},
	ProviderKimi:       {"MOONSHOT_API_KEY", "KIMI_API_KEY"},
	ProviderKimiCode:   {"KIMI_CODE_API_KEY", "MOONSHOT_API_KEY"},
	ProviderZai:        {"ZAI_API_KEY"},
	ProviderZaiApi:     {"ZAI_API_KEY"},
}

// localProviders need no API key.
var localProviders = map[Provider]bool{
	ProviderLMStudio: true,
	ProviderOllama:   true,
}

// envVarsForProvider returns the environment variable names to check for a
// given provider, in priority order. Returns nil for local-only providers.
func envVarsForProvider(provider Provider) []string {
	if localProviders[provider] {
		return nil
	}
	if vars, ok := providerEnvVars[provider]; ok {
		return vars
	}
	// Generic fallback: {PROVIDER_UPPER}_API_KEY
	key := toUpperSnakeCase(string(provider)) + "_API_KEY"
	return []string{key}
}

// toUpperSnakeCase converts a string to UPPER_SNAKE_CASE.
// Examples: "my-provider" → "MY_PROVIDER", "openRouter" → "OPEN_ROUTER".
// Handles: dashes, dots, camelCase, and already-uppercase strings.
func toUpperSnakeCase(s string) string {
	if s == "" {
		return ""
	}

	var result strings.Builder
	prevLower := false
	for i, c := range s {
		if c >= 'a' && c <= 'z' {
			result.WriteRune(c - 'a' + 'A')
			prevLower = true
		} else if c >= 'A' && c <= 'Z' {
			if i > 0 && prevLower {
				result.WriteRune('_')
			}
			result.WriteRune(c)
			prevLower = false
		} else if c == '-' || c == '.' || c == ' ' {
			result.WriteRune('_')
			prevLower = false
		} else {
			result.WriteRune(c)
			prevLower = false
		}
	}
	return result.String()
}
