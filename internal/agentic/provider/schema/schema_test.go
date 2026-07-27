// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func TestLoadEmbeddedProfiles(t *testing.T) {
	profiles, err := LoadEmbeddedProfiles()
	require.NoError(t, err)
	require.NotEmpty(t, profiles)

	ids := make(map[string]bool)
	for _, p := range profiles {
		ids[p.ID] = true
	}
	assert.True(t, ids["openai-base"], "expected openai-base profile")
	assert.True(t, ids["anthropic-base"], "expected anthropic-base profile")
	assert.True(t, ids["google-base"], "expected google-base profile")
}

func TestResolveProfile_KimiProvidersHaveAuth(t *testing.T) {
	for _, prov := range []Provider{ProviderKimi, ProviderKimiCode} {
		profile := ResolveProfile(Model{
			ID:       "test-model",
			Api:      ApiOpenAICompletions,
			Provider: prov,
		})
		assert.Equal(t, "Authorization", profile.Auth.Header, "provider %q should use Authorization header", prov)
		assert.Equal(t, "Bearer ", profile.Auth.Prefix, "provider %q should use Bearer prefix", prov)
		assert.Equal(t, AuthMethodAPIKey, profile.Auth.Method, "provider %q should use api_key auth", prov)
	}
}

// TestResolveProfile_UnmatchedProvidersGetDefaultAuth is the regression test for
// bugs.md Issue 9: providers without a dedicated variant profile (poolside, groq,
// fireworks, perplexity, zai-api, custom) previously resolved to an empty profile,
// so AuthHook dropped the API key and the request went out with no Authorization
// header (401). The resolver must now fall back to a sane default profile that
// includes standard Bearer authentication.
func TestResolveProfile_UnmatchedProvidersGetDefaultAuth(t *testing.T) {
	unmatched := []Provider{
		ProviderPoolside, ProviderGroq, ProviderFireworks, ProviderPerplexity,
		ProviderZaiApi, ProviderCustom, Provider("some-future-provider"),
	}
	for _, prov := range unmatched {
		profile := ResolveProfile(Model{
			ID:       "test-model",
			Api:      ApiOpenAICompletions,
			Provider: prov,
			BaseURL:  "https://example.test/v1",
		})
		assert.Equal(t, "default-openai-compat", profile.ID, "provider %q should use the default profile", prov)
		assert.Equal(t, "Authorization", profile.Auth.Header, "provider %q must inject Authorization header", prov)
		assert.Equal(t, "Bearer ", profile.Auth.Prefix, "provider %q must use Bearer prefix", prov)
		assert.Equal(t, AuthMethodAPIKey, profile.Auth.Method, "provider %q must use api_key auth", prov)
	}
}

// TestDefaultProfile_SaneBaseline pins the baseline feature set so the default
// cannot silently regress to dropping auth or emitting a non-standard field.
func TestDefaultProfile_SaneBaseline(t *testing.T) {
	p := DefaultProfile(Model{Api: ApiOpenAICompletions, Provider: ProviderPoolside, BaseURL: "https://inference.poolside.ai/v1"})

	// Auth is the critical bit.
	assert.Equal(t, "Authorization", p.Auth.Header)
	assert.Equal(t, "Bearer ", p.Auth.Prefix)
	assert.Equal(t, AuthMethodAPIKey, p.Auth.Method)
	assert.False(t, p.Auth.Required, "default must not hard-require a key (local/custom endpoints)")

	// Sane OpenAI-compatible compat/tool/error defaults.
	assert.Equal(t, "max_tokens", p.Compat.MaxTokensField)
	assert.Equal(t, "openai", p.Compat.ThinkingFormat)
	assert.True(t, p.Compat.StreamIncludesUsage)
	assert.Equal(t, SchemaSanitizerOpenAI, p.ToolCompat.SchemaSanitizer)
	assert.Equal(t, CacheModeNone, p.CachePolicy.Mode)
	assert.Equal(t, []int{408, 429, 500, 502, 503, 504}, p.ErrorRules.RetryableStatuses)

	// Match echoes the model so the synthesized profile is attributable.
	assert.Equal(t, string(ProviderPoolside), p.Match.Provider)
}

// TestResolver_NoMatchFallsBackToDefault verifies Resolver.Resolve returns the
// default (not a zero profile) when nothing matches, while still preferring a
// real match when one exists.
func TestResolver_NoMatchFallsBackToDefault(t *testing.T) {
	r := NewResolver([]VariantProfile{
		{ID: "openai-base", Match: ProfileMatch{API: string(ApiOpenAICompletions), Provider: "openai"}},
	})

	// No match -> default with auth.
	noMatch := r.Resolve(Model{Api: ApiOpenAICompletions, Provider: ProviderPoolside, ID: "laguna-s-2-1"})
	assert.Equal(t, "default-openai-compat", noMatch.ID)
	assert.Equal(t, "Authorization", noMatch.Auth.Header)
	assert.False(t, noMatch.IsEmpty(), "default profile must not be 'empty'")

	// A real match still wins over the default.
	match := r.Resolve(Model{Api: ApiOpenAICompletions, Provider: ProviderOpenAI, ID: "gpt-4o"})
	assert.Equal(t, "openai-base", match.ID)
}

func TestLoadEmbeddedProfile(t *testing.T) {
	p, err := LoadEmbeddedProfile("openai-base")
	require.NoError(t, err)
	assert.Equal(t, "openai-base", p.ID)
	assert.Equal(t, string(ApiOpenAICompletions), p.Match.API)
	assert.Equal(t, "openai", p.Match.Provider)
	assert.NotNil(t, p.Defaults.Temperature)
	assert.InDelta(t, 1.0, *p.Defaults.Temperature, 0.0001)
}

func TestLoadEmbeddedProfile_OpenAIEnablesPromptCache(t *testing.T) {
	p, err := LoadEmbeddedProfile("openai-base")
	require.NoError(t, err)
	assert.Equal(t, "openai-base", p.ID)
	assert.True(t, p.Compat.SupportsPromptCache, "OpenAI should advertise prompt-cache support")
}

func TestLoadEmbeddedProfile_OpenRouterEnablesPromptCache(t *testing.T) {
	p, err := LoadEmbeddedProfile("openrouter")
	require.NoError(t, err)
	assert.Equal(t, "openrouter", p.ID)
	assert.Equal(t, "short", string(p.CachePolicy.Mode), "OpenRouter should use short cache policy to emit Anthropic-style cache_control breakpoints")
	assert.True(t, p.Compat.SupportsPromptCache, "OpenRouter should advertise prompt-cache support")
}

func TestLoadEmbeddedProfile_LMStudioEnablesPromptCache(t *testing.T) {
	p, err := LoadEmbeddedProfile("lm-studio")
	require.NoError(t, err)
	assert.Equal(t, "lm-studio", p.ID)
	assert.Equal(t, "short", string(p.CachePolicy.Mode), "LM Studio should use short cache policy to emit Anthropic-style cache_control breakpoints")
	assert.True(t, p.Compat.SupportsPromptCache, "LM Studio should advertise prompt-cache support")
}

func TestLoadEmbeddedProfile_OllamaEnablesPromptCache(t *testing.T) {
	p, err := LoadEmbeddedProfile("ollama")
	require.NoError(t, err)
	assert.Equal(t, "ollama", p.ID)
	assert.Equal(t, "short", string(p.CachePolicy.Mode), "Ollama should use short cache policy to emit Anthropic-style cache_control breakpoints")
	assert.True(t, p.Compat.SupportsPromptCache, "Ollama should advertise prompt-cache support")
}

func TestLoadEmbeddedProfileNotFound(t *testing.T) {
	_, err := LoadEmbeddedProfile("does-not-exist")
	require.Error(t, err)
}

func TestResolverSelectsMostSpecific(t *testing.T) {
	profiles := []VariantProfile{
		{ID: "openai-base", Match: ProfileMatch{API: string(ApiOpenAICompletions), Provider: "openai"}},
		{ID: "openai-gpt4o", Match: ProfileMatch{API: string(ApiOpenAICompletions), Provider: "openai", ModelID: "gpt-4o"}},
	}
	r := NewResolver(profiles)

	generic := r.Resolve(Model{Api: ApiOpenAICompletions, Provider: ProviderOpenAI, ID: "gpt-3.5"})
	assert.Equal(t, "openai-base", generic.ID)

	specific := r.Resolve(Model{Api: ApiOpenAICompletions, Provider: ProviderOpenAI, ID: "gpt-4o"})
	assert.Equal(t, "openai-gpt4o", specific.ID)
}

func TestResolverVariantIDOverride(t *testing.T) {
	profiles := []VariantProfile{
		{ID: "openai-base", Match: ProfileMatch{API: string(ApiOpenAICompletions), Provider: "openai"}},
		{ID: "custom-openai", Match: ProfileMatch{VariantID: "custom"}},
	}
	r := NewResolver(profiles)
	m := r.Resolve(Model{Api: ApiOpenAICompletions, Provider: ProviderOpenAI, VariantID: "custom"})
	assert.Equal(t, "custom-openai", m.ID)
}

func TestResolveURLTemplate(t *testing.T) {
	t.Setenv("GOA_TEST_HOST", "api.example.com")
	t.Setenv("GOA_TEST_PATH", "v1")

	assert.Equal(t, "https://api.example.com/v1/chat", ResolveURLTemplate("https://{GOA_TEST_HOST}/{GOA_TEST_PATH}/chat"))
	assert.Equal(t, "https://{MISSING}/chat", ResolveURLTemplate("https://{MISSING}/chat"))
}

func TestMergeProfiles(t *testing.T) {
	base := VariantProfile{
		ID:       "base",
		Match:    ProfileMatch{API: string(ApiOpenAICompletions), Provider: "openai"},
		Defaults: Defaults{Temperature: ptr(1.0)},
		Compat:   CompatFlags{SupportsStore: ptr(true), ThinkingFormat: "openai"},
		Auth:     AuthConfig{Method: AuthMethodAPIKey, Header: "Authorization"},
	}
	override := VariantProfile{
		ID:       "override",
		Compat:   CompatFlags{SupportsStore: ptr(false), ThinkingFormat: "deepseek"},
		Defaults: Defaults{MaxTokens: ptr(8192)},
	}

	merged := MergeProfiles(base, override)
	assert.Equal(t, "override", merged.ID)
	assert.Equal(t, "deepseek", merged.Compat.ThinkingFormat)
	assert.Equal(t, false, *merged.Compat.SupportsStore)
	assert.Equal(t, 8192, *merged.Defaults.MaxTokens)
	require.NotNil(t, merged.Defaults.Temperature)
	assert.InDelta(t, 1.0, *merged.Defaults.Temperature, 0.0001)
	assert.Equal(t, "Authorization", merged.Auth.Header)
}

func TestLoadUserProfiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	profileDir := filepath.Join(dir, ".goa", "providers")
	require.NoError(t, os.MkdirAll(profileDir, 0o755))

	data := []byte(`{
		"id": "my-deepseek",
		"match": {"api": "openai-completions", "provider": "deepseek"},
		"compat": {"thinking_format": "deepseek"}
	}`)
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "deepseek.json"), data, 0o644))

	profiles, err := LoadUserProfiles()
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "my-deepseek", profiles[0].ID)
	assert.Equal(t, "deepseek", profiles[0].Compat.ThinkingFormat)
}

func TestApplyTemplate(t *testing.T) {
	env := map[string]any{
		"model": map[string]any{"id": "gpt-4o"},
		"key":   "secret",
	}
	assert.Equal(t, "model=gpt-4o key=secret", ApplyTemplate("model=$model.id key=$key", env))
	assert.Equal(t, "missing=$UNKNOWN ${UNKNOWN}", ApplyTemplate("missing=$UNKNOWN ${UNKNOWN}", env))
}

func TestEvalExpression(t *testing.T) {
	env := map[string]any{"x": 10, "y": 20}
	v, err := EvalExpression("x + y", env)
	require.NoError(t, err)
	assert.Equal(t, 30, v)
}