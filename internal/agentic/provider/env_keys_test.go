// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func TestGetEnvAPIKey_MalformedKeyIsInvalidCredential(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  string
	}{
		{name: "interior space", val: "sk bad key"},
		{name: "whitespace only", val: "   "},
		{name: "unicode", val: "sk-ключ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("OPENAI_API_KEY", tc.val)
			defer os.Unsetenv("OPENAI_API_KEY")

			key, err := GetEnvAPIKey(ProviderOpenAI)
			if key != "" {
				t.Errorf("expected empty key, got %q", key)
			}
			var inv *schema.InvalidCredentialError
			if !errors.As(err, &inv) {
				t.Fatalf("expected *schema.InvalidCredentialError, got %T: %v", err, err)
			}
			if inv.Source != "OPENAI_API_KEY" {
				t.Errorf("Source = %q, want OPENAI_API_KEY", inv.Source)
			}
			if strings.Contains(err.Error(), tc.val) {
				t.Errorf("error must never include the key material: %q", err.Error())
			}
		})
	}
}

func TestGetEnvAPIKey_TrimsWhitespace(t *testing.T) {
	os.Setenv("DEEPSEEK_API_KEY", "  sk-deep-ok  ")
	defer os.Unsetenv("DEEPSEEK_API_KEY")

	key, err := GetEnvAPIKey(ProviderDeepSeek)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-deep-ok" {
		t.Errorf("expected trimmed key, got %q", key)
	}
}

func TestGetEnvAPIKey_MissingReturnsNilError(t *testing.T) {
	os.Unsetenv("DEEPSEEK_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	key, err := GetEnvAPIKey(ProviderDeepSeek)
	if err != nil {
		t.Fatalf("missing key must be (\"\", nil), got error: %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestEnvVarsForProvider(t *testing.T) {
	got := EnvVarsForProvider(ProviderAnthropic)
	if len(got) < 2 || got[0] != "ANTHROPIC_OAUTH_TOKEN" || got[1] != "ANTHROPIC_API_KEY" {
		t.Errorf("EnvVarsForProvider(anthropic) = %v, want OAUTH then API key", got)
	}
	if EnvVarsForProvider(ProviderLMStudio) != nil {
		t.Errorf("local providers must report no env vars")
	}
}
