// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// IsCuratedOverride is the wire-format probe's pin signal: any model covered
// by a hand-maintained override (exact ID or prefix) is "curated" and must
// never be probed, because the override authoritatively chose its wire API.
// Catalog (models.dev) entries are NOT pins. The exact-ID-first ordering
// matters: a carve-out like qwen3.6-plus-free must register as curated even
// though the qwen3.6- prefix also matches it.
func TestIsCuratedOverride(t *testing.T) {
	tests := []struct {
		name string
		prov provider.Provider
		id   string
		want bool
	}{
		// Exact-ID anthropic entry on zen Go.
		{"union-alpha exact @ opencode-go", provider.ProviderOpenCodeGo, "union-alpha", true},
		// Prefix matches.
		{"qwen3.6-plus prefix @ opencode-go", provider.ProviderOpenCodeGo, "qwen3.6-plus", true},
		{"qwen3.6-max prefix @ opencode", provider.ProviderOpenCode, "qwen3.6-max", true},
		// Exact-ID carve-out beats the qwen3.6- prefix (last-write-wins in the
		// registry, but IsCuratedOverride must report it curated regardless).
		{"qwen3.6-plus-free carve-out @ opencode", provider.ProviderOpenCode, "qwen3.6-plus-free", true},
		// Metadata-only entries are still curated pins.
		{"deepseek-v4-flash metadata-only", provider.ProviderOpenCode, "deepseek-v4-flash", true},
		// Case-insensitivity.
		{"UNION-ALPHA case-insensitive", provider.ProviderOpenCodeGo, "UNION-ALPHA", true},
		// Negatives: no override coverage.
		{"omen-alpha not overridden (oa-compat)", provider.ProviderOpenCodeGo, "omen-alpha", false},
		{"unknown model", provider.ProviderOpenCodeGo, "no-such-model-xyz", false},
		{"unknown provider", provider.Provider("nonexistent"), "union-alpha", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCuratedOverride(tc.prov, tc.id); got != tc.want {
				t.Errorf("IsCuratedOverride(%q, %q) = %v, want %v", tc.prov, tc.id, got, tc.want)
			}
		})
	}
}
