// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"fmt"
	"strings"
)

// Credential errors (gap DS5, P15). Two distinct kinds — missing versus
// malformed — so callers can tell a configuration gap apart from a bad value,
// mirroring the deepseek-harness api-key rejection model
// (packages/llm/llm/src/api-key.ts). Every error names the config entry point
// the key was resolved from and never the key material itself.

// InvalidCredentialError reports a resolved-but-malformed API key. Source names
// the config entry point the key came from (an env var name such as
// "OPENAI_API_KEY" or the explicit "options.api_key"); Reason describes the
// defect. The key material is never included.
type InvalidCredentialError struct {
	Source string
	Reason string
}

// Error implements error.
func (e *InvalidCredentialError) Error() string {
	if e == nil {
		return "invalid API key"
	}
	return fmt.Sprintf("invalid API key in %s: %s", e.Source, e.Reason)
}

// MissingCredentialError reports that no credential could be resolved.
// Provider names the model provider; Sources lists every env var / config
// entry point that was checked.
type MissingCredentialError struct {
	Provider string
	Sources  []string
}

// Error implements error.
func (e *MissingCredentialError) Error() string {
	if e == nil {
		return "no API key found"
	}
	return fmt.Sprintf("no API key found for provider %q: checked %s",
		e.Provider, strings.Join(e.Sources, ", "))
}

// ValidateAPIKey validates a resolved API key value before first use, mirroring
// dsh's normalizeApiKey (packages/llm/llm/src/api-key.ts): surrounding
// whitespace is trimmed silently, then the key must be non-empty printable
// ASCII (0x21..0x7E) with no spaces. source names the config entry point the
// raw value was resolved from and is used verbatim in the returned
// *InvalidCredentialError; the key material itself is never included in
// errors. On success the trimmed key is returned.
func ValidateAPIKey(source, raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", &InvalidCredentialError{Source: source, Reason: "key is empty after trimming whitespace"}
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x21 || c > 0x7E {
			return "", &InvalidCredentialError{Source: source, Reason: "contains characters outside printable ASCII (spaces not allowed)"}
		}
	}
	return key, nil
}
