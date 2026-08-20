// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthHook_InvalidOptionsAPIKey verifies a malformed explicitly-configured
// key is rejected as InvalidCredential before any header injection, naming the
// config entry point and never the key.
func TestAuthHook_InvalidOptionsAPIKey(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer "},
	}))

	secret := "sk-bad key material"
	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderOpenAI},
		Options: schema.StreamOptions{APIKey: secret},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	var inv *schema.InvalidCredentialError
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, "options.api_key", inv.Source)
	assert.NotContains(t, err.Error(), secret, "error must never include the key material")
	assert.Empty(t, ctx.Headers["Authorization"], "no auth header may be set for an invalid key")
}

// TestAuthHook_InvalidProfileEnvKey verifies a malformed key resolved from a
// profile env var is rejected as InvalidCredential naming the variable.
func TestAuthHook_InvalidProfileEnvKey(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header:   "Authorization",
			Prefix:   "Bearer ",
			EnvVars:  []string{"OPENAI_API_KEY"},
			Required: true,
		},
	}))

	secret := "sk-\u00a0nbsp"
	os.Setenv("OPENAI_API_KEY", secret)
	defer os.Unsetenv("OPENAI_API_KEY")

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderOpenAI},
		Options: schema.StreamOptions{},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	var inv *schema.InvalidCredentialError
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, "OPENAI_API_KEY", inv.Source)
	assert.NotContains(t, err.Error(), "nbsp", "error must never include the key material")
	assert.Empty(t, ctx.Headers["Authorization"])
}

// TestAuthHook_MissingRequiredCredential verifies a required-but-absent key
// yields MissingCredential listing every env var/config source checked.
func TestAuthHook_MissingRequiredCredential(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header:   "Authorization",
			Prefix:   "Bearer ",
			EnvVars:  []string{"DEEPSEEK_API_KEY"},
			Required: true,
		},
	}))

	os.Unsetenv("DEEPSEEK_API_KEY")

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderDeepSeek},
		Options: schema.StreamOptions{},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	var miss *schema.MissingCredentialError
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "deepseek", miss.Provider)
	require.Equal(t, []string{"options.api_key", "DEEPSEEK_API_KEY"}, miss.Sources)
	assert.Contains(t, err.Error(), "DEEPSEEK_API_KEY")
	assert.Empty(t, ctx.Headers["Authorization"])
}

// TestAuthHook_MissingNonRequiredIsSilent preserves the local/custom endpoint
// contract: a provider that does not require a credential sends the request
// without one instead of failing.
func TestAuthHook_MissingNonRequiredIsSilent(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header:  "Authorization",
			Prefix:  "Bearer ",
			EnvVars: []string{"LLAMA_API_KEY"},
		},
	}))

	os.Unsetenv("LLAMA_API_KEY")

	ctx := &RequestContext{
		Model:   schema.Model{Provider: "llama-cpp"},
		Options: schema.StreamOptions{},
		Headers: make(map[string]string),
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Empty(t, ctx.Headers["Authorization"])
}

// TestAuthHook_EnvKeyTrimmed verifies a padded env key is trimmed before use
// (mirrors dsh normalizeApiKey silent trim).
func TestAuthHook_EnvKeyTrimmed(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer ", EnvVars: []string{"OPENAI_API_KEY"}},
	}))

	os.Setenv("OPENAI_API_KEY", "  sk-trimmed  ")
	defer os.Unsetenv("OPENAI_API_KEY")

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderOpenAI},
		Options: schema.StreamOptions{},
		Headers: make(map[string]string),
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Equal(t, "Bearer sk-trimmed", ctx.Headers["Authorization"])
}

// TestAuthHook_ErrorMessagesNeverContainKeyMaterial is a belt-and-braces check
// across both error kinds: run the full error strings through the same
// "no secret substring" assertion.
func TestAuthHook_ErrorMessagesNeverContainKeyMaterial(t *testing.T) {
	secret := "sk-ultra-secret-abc"
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", EnvVars: []string{"OPENAI_API_KEY"}, Required: true},
	}))

	os.Setenv("OPENAI_API_KEY", secret+" extra")
	defer os.Unsetenv("OPENAI_API_KEY")

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderOpenAI},
		Options: schema.StreamOptions{},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, err.Error(), "ultra-secret")
	assert.True(t, strings.Contains(err.Error(), "OPENAI_API_KEY") || strings.Contains(err.Error(), "options.api_key"))
}
