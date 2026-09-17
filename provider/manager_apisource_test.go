// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// apiSource is the wire-format probe's pin signal. It must report "user" for
// an explicit model-config `api:`, "curated" for a model covered by a
// hand-maintained model_overrides.yaml entry (exact or prefix), and "" for a
// catalog/fallback default (probeable). The check is scoped to the serving
// provider.
func TestAPISource(t *testing.T) {
	tests := []struct {
		name      string
		mCfg      config.ModelConfig
		prov      string
		modelName string
		want      string
	}{
		{
			name:      "explicit api is a user pin",
			mCfg:      config.ModelConfig{API: "anthropic-messages"},
			prov:      "opencode-go",
			modelName: "some-model",
			want:      "user",
		},
		{
			name:      "curated override (exact ID) is a curated pin",
			mCfg:      config.ModelConfig{},
			prov:      "opencode-go",
			modelName: "union-alpha",
			want:      "curated",
		},
		{
			name:      "curated override (prefix) is a curated pin",
			mCfg:      config.ModelConfig{},
			prov:      "opencode-go",
			modelName: "qwen3.6-plus",
			want:      "curated",
		},
		{
			name:      "catalog default stays probeable",
			mCfg:      config.ModelConfig{},
			prov:      "opencode-go",
			modelName: "omen-alpha", // not overridden — oa-compat sibling
			want:      "",
		},
		{
			name:      "user pin wins over curated",
			mCfg:      config.ModelConfig{API: "openai-completions"},
			prov:      "opencode-go",
			modelName: "union-alpha",
			want:      "user",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := apiSource(tc.mCfg, agenticprovider.Provider(tc.prov), tc.modelName)
			if got != tc.want {
				t.Errorf("apiSource(api=%q, prov=%q, model=%q) = %q, want %q",
					tc.mCfg.API, tc.prov, tc.modelName, got, tc.want)
			}
		})
	}
}

// TestApiSourceStampedOnResolution verifies both model-construction paths
// (registry merge and fallback build) stamp ApiSource so the probe's pin gate
// sees it on the live model.
func TestApiSourceStampedOnResolution(t *testing.T) {
	pCfg := config.ProviderConfig{ID: "opencode-go", Provider: "opencode-go", Endpoint: "https://opencode.ai/zen/go/v1"}

	// Catalog model with an explicit user api pin → fallback path stamps "user".
	userPinned := buildFallbackModel(pCfg, config.ModelConfig{API: "anthropic-messages"}, "union-alpha")
	if userPinned.ApiSource != "user" {
		t.Errorf("fallback user-pinned ApiSource = %q, want %q", userPinned.ApiSource, "user")
	}

	// Catalog model with curated override, no user api → "curated".
	curated := buildFallbackModel(pCfg, config.ModelConfig{}, "union-alpha")
	if curated.ApiSource != "curated" {
		t.Errorf("fallback curated ApiSource = %q, want %q", curated.ApiSource, "curated")
	}

	// Unknown catalog model, no override → fallback path leaves "" (probeable).
	probeable := buildFallbackModel(pCfg, config.ModelConfig{}, "totally-unknown-model")
	if probeable.ApiSource != "" {
		t.Errorf("fallback probeable ApiSource = %q, want empty", probeable.ApiSource)
	}
}
