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

// writeUserProfile lays a user-config profile into $GOA_HOME/.goa/providers
// (the layer the cascade reads first).
func writeUserProfile(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, userConfigDir, providersSubdir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// TestResolveProfile_UserProfileLayersOverProviderProfile pins review finding
// F5: a user profile that pins one field for one model must REFINE the
// provider profile, not replace it. Replacing silently dropped every field the
// override did not restate — max_tokens_field and
// requires_reasoning_content_on_assistant_messages among them — so a targeted
// fix became a multi-turn failure. docs/PROVIDERS.md documents the merge
// cascade these tests now enforce.
func TestResolveProfile_UserProfileLayersOverProviderProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)
	t.Chdir(t.TempDir()) // isolate the project/local layers

	writeUserProfile(t, home, "catjam.json", `{
	  "id": "catjam-effort-only",
	  "match": { "provider": "opencode-go", "model_id": "cat-jam-free" },
	  "compat": { "thinking_format": "none" }
	}`)

	model := Model{
		ID:       "cat-jam-free",
		Api:      "openai-completions",
		Provider: "opencode-go",
		BaseURL:  "https://opencode.ai/zen/go/v1",
	}
	profile := ResolveProfile(model)

	// The override wins for the field it names ...
	assert.Equal(t, "none", profile.Compat.ThinkingFormat)
	// ... and everything it did not restate survives from the provider profile.
	assert.True(t, profile.Compat.RequiresReasoningContentOnAssistantMessages,
		"the reasoning_content echo requirement must survive a targeted override")
	assert.Equal(t, "max_tokens", profile.Compat.MaxTokensField)
	assert.Equal(t, "api_key", string(profile.Auth.Method))
	assert.True(t, profile.Auth.Required)
	assert.Equal(t, []string{"OPENCODE_API_KEY"}, profile.Auth.EnvVars)
	// The effective profile names the layers that built it, for GOA_DEBUG_PROVIDER=1.
	assert.Equal(t, "opencode-go+catjam-effort-only", profile.ID)
}

// TestResolveProfile_CascadeOrderPutsLocalLast pins the documented order:
// embedded < user < project < local, so the local layer has the final word.
func TestResolveProfile_CascadeOrderPutsLocalLast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)
	cwd := t.TempDir()
	t.Chdir(cwd)

	writeUserProfile(t, home, "layer.json", `{
	  "id": "user-layer",
	  "match": { "provider": "opencode-go" },
	  "compat": { "thinking_format": "user-value" }
	}`)

	projectDir := filepath.Join(cwd, projectConfigDir, providersSubdir)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "layer.json"), []byte(`{
	  "id": "project-layer",
	  "match": { "provider": "opencode-go" },
	  "compat": { "thinking_format": "project-value" }
	}`), 0o644))

	localDir := filepath.Join(cwd, localConfigDir, providersSubdir+".local")
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "layer.json"), []byte(`{
	  "id": "local-layer",
	  "match": { "provider": "opencode-go" },
	  "compat": { "thinking_format": "local-value" }
	}`), 0o644))

	profile := ResolveProfile(Model{
		ID:       "some-model",
		Api:      "openai-completions",
		Provider: "opencode-go",
		BaseURL:  "https://opencode.ai/zen/go/v1",
	})

	assert.Equal(t, "local-value", profile.Compat.ThinkingFormat)
	assert.Equal(t, "opencode-go+user-layer+project-layer+local-layer", profile.ID)
}

// TestResolveProfile_SpecificityDecidesWithinALayer keeps the intra-layer
// rule: one source contributes its most specific match only.
func TestResolveProfile_SpecificityDecidesWithinALayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)
	t.Chdir(t.TempDir())

	writeUserProfile(t, home, "broad.json", `{
	  "id": "broad",
	  "match": { "provider": "opencode-go" },
	  "compat": { "thinking_format": "broad-value" },
	  "defaults": { "max_tokens": 111 }
	}`)
	writeUserProfile(t, home, "narrow.json", `{
	  "id": "narrow",
	  "match": { "provider": "opencode-go", "model_id": "cat-jam-free" },
	  "compat": { "thinking_format": "narrow-value" }
	}`)

	profile := ResolveProfile(Model{
		ID:       "cat-jam-free",
		Api:      "openai-completions",
		Provider: "opencode-go",
		BaseURL:  "https://opencode.ai/zen/go/v1",
	})

	assert.Equal(t, "narrow-value", profile.Compat.ThinkingFormat)
	assert.Equal(t, "opencode-go+narrow", profile.ID,
		"the less specific profile in the same layer must not also merge")
	require.NotNil(t, profile.Defaults.MaxTokens)
	assert.Equal(t, 8192, *profile.Defaults.MaxTokens,
		"the provider default survives: 'broad' did not win its own layer")
}

// TestResolver_SingleLayerReturnsProfileVerbatim is the guard for the merge
// helpers. A field the helpers forget to copy is silently dropped the moment a
// profile is merged — exactly how Auth.Required and the plain-bool compat flags
// went missing when layering was introduced. With one source, resolution must
// therefore return the declared profile untouched.
func TestResolver_SingleLayerReturnsProfileVerbatim(t *testing.T) {
	embedded, err := LoadEmbeddedProfiles()
	require.NoError(t, err)
	require.NotEmpty(t, embedded)

	r := NewResolver(embedded)
	for _, want := range embedded {
		t.Run(want.ID, func(t *testing.T) {
			got := r.Resolve(modelForMatch(want.Match))
			assert.Equal(t, want, got)
		})
	}
}

// modelForMatch synthesizes a model that the profile's match accepts, filling
// unconstrained dimensions with values no other profile narrows.
func modelForMatch(m ProfileMatch) Model {
	model := Model{
		ID:        m.ModelID,
		Api:       Api(m.API),
		Provider:  Provider(m.Provider),
		BaseURL:   m.BaseURL,
		VariantID: m.VariantID,
	}
	if model.ID == "" {
		model.ID = "probe-model"
	}
	if model.Api == "" {
		model.Api = ApiOpenAICompletions
	}
	if model.Provider == "" {
		model.Provider = "probe-provider"
	}
	return model
}
