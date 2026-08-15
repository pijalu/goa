// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// GetEnvAPIKey returns the validated API key for the given provider by
// checking well-known environment variables (DS5/P15).
//
// It returns ("", nil) when no known environment variable is set, or when the
// provider doesn't need an API key (e.g., local providers like LM Studio).
// When a set variable holds a malformed key (empty after trimming, or
// containing characters outside printable ASCII) it returns an
// *schema.InvalidCredentialError naming the variable — the key material is
// never included. The absence case is reported as ("", nil); callers that
// must distinguish "missing" from "invalid" should inspect the error, while
// callers that only need a key may treat any error as "no usable key".
func GetEnvAPIKey(provider Provider) (string, error) {
	vars := envVarsForProvider(provider)
	for _, v := range vars {
		if raw := os.Getenv(v); raw != "" {
			key, err := schema.ValidateAPIKey(v, raw)
			if err != nil {
				return "", err
			}
			return key, nil
		}
	}
	return "", nil
}

// EnvVarsForProvider returns the environment variable names GetEnvAPIKey
// checks for the given provider, in priority order. Returns nil for local-only
// providers. Used by callers building a MissingCredentialError that lists
// every source checked.
func EnvVarsForProvider(provider Provider) []string {
	return envVarsForProvider(provider)
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
