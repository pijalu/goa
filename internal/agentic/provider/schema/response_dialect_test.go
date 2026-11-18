// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveProfile_ResponseDialectHy4ExactMatch(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  string
	}{
		{
			name:  "exact OpenAI completions model",
			model: Model{ID: "hy4-flash-free", Api: ApiOpenAICompletions, Provider: ProviderOpenCodeGo},
			want:  ResponseDialectHy4,
		},
		{
			name:  "other provider model remains off",
			model: Model{ID: "deepseek-v4-flash", Api: ApiOpenAICompletions, Provider: ProviderOpenCodeGo},
			want:  "",
		},
		{
			name:  "model prefix is not enough",
			model: Model{ID: "hy4-flash-free-preview", Api: ApiOpenAICompletions, Provider: ProviderOpenCodeGo},
			want:  "",
		},
		{
			name:  "other API remains off",
			model: Model{ID: "hy4-flash-free", Api: ApiOpenAIResponses, Provider: ProviderOpenCodeGo},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveProfile(tt.model).Compat.ResponseDialect)
		})
	}
}

func TestMergeProfiles_ResponseDialectExplicitNoneWins(t *testing.T) {
	base := VariantProfile{Compat: CompatFlags{ResponseDialect: ResponseDialectHy4}}
	assert.Equal(t, ResponseDialectHy4, MergeProfiles(base, VariantProfile{}).Compat.ResponseDialect)
	assert.Equal(t, ResponseDialectNone, MergeProfiles(base, VariantProfile{
		Compat: CompatFlags{ResponseDialect: ResponseDialectNone},
	}).Compat.ResponseDialect)
}

func TestResolveProfile_ResponseDialectLayeredNoneOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)
	t.Chdir(t.TempDir())
	writeUserProfile(t, home, "hy4.json", `{
	  "id": "disable-hy4-dialect",
	  "match": {"provider": "opencode-go", "model_id": "hy4-flash-free"},
	  "compat": {"response_dialect": "none"}
	}`)

	profile := ResolveProfile(Model{
		ID: "hy4-flash-free", Api: ApiOpenAICompletions, Provider: ProviderOpenCodeGo,
	})
	assert.Equal(t, ResponseDialectNone, profile.Compat.ResponseDialect)
	assert.True(t, profile.Compat.RequiresReasoningContentOnAssistantMessages,
		"the targeted dialect override must retain the OpenCode profile")
}
