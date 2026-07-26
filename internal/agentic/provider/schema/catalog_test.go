// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import "testing"

// TestProviderCatalog_NoDuplicateIDs ensures the catalog is a valid single
// source of truth: no two entries share an ID or Provider identity.
func TestProviderCatalog_NoDuplicateIDs(t *testing.T) {
	ids := map[string]bool{}
	provs := map[Provider]bool{}
	for _, d := range ProviderCatalog() {
		if d.ID == "" {
			t.Errorf("catalog entry has empty ID: %+v", d)
		}
		if ids[d.ID] {
			t.Errorf("duplicate catalog ID %q", d.ID)
		}
		ids[d.ID] = true
		if d.Provider != "" {
			if provs[d.Provider] {
				t.Errorf("duplicate catalog Provider %q", d.Provider)
			}
			provs[d.Provider] = true
		}
	}
}

// TestLookupProviderDef round-trips every catalog entry through both lookup
// helpers, proving ID and Provider identity stay consistent.
func TestLookupProviderDef(t *testing.T) {
	for _, d := range ProviderCatalog() {
		if got := LookupProviderDefByID(d.ID); got == nil || got.ID != d.ID {
			t.Errorf("LookupProviderDefByID(%q) = %v, want entry", d.ID, got)
		}
		if d.Provider != "" {
			if got := LookupProviderDef(d.Provider); got == nil || got.Provider != d.Provider {
				t.Errorf("LookupProviderDef(%q) = %v, want entry", d.Provider, got)
			}
		}
	}
}

// TestMatchProviderByNameOrURL_Poolside proves a catalog-only provider is
// fingerprintable by both its identity and its URL pattern with no dedicated
// code — the invariant behind the template-driven catalog.
func TestMatchProviderByNameOrURL_Poolside(t *testing.T) {
	byName := MatchProviderByNameOrURL(ProviderPoolside, "https://inference.poolside.ai/v1")
	if byName == nil || byName.Provider != ProviderPoolside {
		t.Errorf("match by name = %v, want poolside", byName)
	}
	byURL := MatchProviderByNameOrURL(ProviderCustom, "https://inference.poolside.ai/v1")
	if byURL == nil || byURL.Provider != ProviderPoolside {
		t.Errorf("match by URL = %v, want poolside", byURL)
	}
	if byName != nil && !byName.Compat.NonStandard {
		t.Error("poolside Compat.NonStandard = false, want true")
	}
}

// TestMatchProviderByNameOrURL_ZaiPrecedence pins the substring-superset
// precedence: the coding URL must resolve to the coding identity.
func TestMatchProviderByNameOrURL_ZaiPrecedence(t *testing.T) {
	coding := MatchProviderByNameOrURL(ProviderCustom, "https://api.z.ai/api/coding/paas/v4")
	if coding == nil || coding.Provider != ProviderZai {
		t.Errorf("coding URL = %v, want zai", coding)
	}
	general := MatchProviderByNameOrURL(ProviderCustom, "https://api.z.ai/api/paas/v4")
	if general == nil || general.Provider != ProviderZaiApi {
		t.Errorf("general URL = %v, want zai-api", general)
	}
}
