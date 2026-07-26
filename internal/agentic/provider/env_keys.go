// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
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
// Derived from the catalog (schema.ProviderCatalog); providers without a
// catalog entry fall back to {PROVIDER_UPPER}_API_KEY in envVarsForProvider.
var providerEnvVars = buildProviderEnvVars()

func buildProviderEnvVars() map[Provider][]string {
	m := make(map[Provider][]string, len(schema.ProviderCatalog()))
	for _, d := range schema.ProviderCatalog() {
		if len(d.EnvKeys) > 0 {
			m[d.Provider] = d.EnvKeys
		}
	}
	return m
}

// localProviders need no API key. Derived from catalog Local flag.
var localProviders = buildLocalProviders()

func buildLocalProviders() map[Provider]bool {
	m := make(map[Provider]bool)
	for _, d := range schema.ProviderCatalog() {
		if d.Compat.Local {
			m[d.Provider] = true
		}
	}
	return m
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
