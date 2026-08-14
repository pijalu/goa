// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"testing"
	"time"
)

// TestPeakStatusAt_DeepSeek pins the DeepSeek peak windows (01:00–04:00 and
// 06:00–10:00 UTC daily) including the 5-minute orange grace margin.
func TestPeakStatusAt_DeepSeek(t *testing.T) {
	def := LookupProviderDefByID("deepseek")
	if def == nil {
		t.Fatal("deepseek not in catalog")
	}
	// 2026-01-15 is a Thursday (UTC).
	at := func(h, m int) time.Time { return time.Date(2026, 1, 15, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name string
		h, m int
		want PeakStatus
	}{
		{"inside first window", 1, 0, PeakOn},
		{"inside first window late", 3, 59, PeakOn},
		{"inside second window", 6, 0, PeakOn},
		{"inside second window late", 9, 59, PeakOn},
		{"just before first window", 0, 57, PeakNear},
		{"just after first window", 4, 3, PeakNear},
		{"just before second window", 5, 57, PeakNear},
		{"just after second window", 10, 3, PeakNear},
		{"far before", 0, 30, PeakOff},
		{"gap between windows", 5, 0, PeakOff},
		{"far after", 23, 0, PeakOff},
	}
	for _, tc := range cases {
		if got := def.PeakStatusAt(at(tc.h, tc.m)); got != tc.want {
			t.Errorf("%s: PeakStatusAt(%02d:%02d) = %v, want %v", tc.name, tc.h, tc.m, got, tc.want)
		}
	}
}

// TestPeakStatusAt_Zai pins the Z.ai weekday peak window (Mon–Fri 14:00–18:00
// SGT == 06:00–10:00 UTC) for both catalog entries, including the weekend and
// the weekday-only grace-margin behavior.
func TestPeakStatusAt_Zai(t *testing.T) {
	for _, id := range []string{"zai", "zai-api"} {
		def := LookupProviderDefByID(id)
		if def == nil {
			t.Fatalf("%s not in catalog", id)
		}
		// 2026-01-15 is a Thursday, 2026-01-18 a Sunday (UTC).
		thu := time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC)
		if got := def.PeakStatusAt(thu); got != PeakOn {
			t.Errorf("%s: Thursday 07:00 UTC = %v, want PeakOn", id, got)
		}
		sun := time.Date(2026, 1, 18, 7, 0, 0, 0, time.UTC)
		if got := def.PeakStatusAt(sun); got != PeakOff {
			t.Errorf("%s: Sunday 07:00 UTC = %v, want PeakOff", id, got)
		}
		// Near margins apply on a weekday (05:55–06:00 before the window).
		near := time.Date(2026, 1, 15, 5, 57, 0, 0, time.UTC)
		if got := def.PeakStatusAt(near); got != PeakNear {
			t.Errorf("%s: Thursday 05:57 UTC = %v, want PeakNear", id, got)
		}
		// The same wall-clock time on Saturday must NOT be near (weekday-only).
		satNear := time.Date(2026, 1, 17, 5, 57, 0, 0, time.UTC)
		if got := def.PeakStatusAt(satNear); got != PeakOff {
			t.Errorf("%s: Saturday 05:57 UTC = %v, want PeakOff", id, got)
		}
	}
}

// TestPeakStatusAt_NoPeakWindows verifies providers without peak windows are
// always PeakOff regardless of the current time.
func TestPeakStatusAt_NoPeakWindows(t *testing.T) {
	def := LookupProviderDefByID("google")
	if def == nil {
		t.Fatal("google not in catalog")
	}
	if got := def.PeakStatusAt(time.Now()); got != PeakOff {
		t.Errorf("google PeakStatusAt(now) = %v, want PeakOff", got)
	}
}

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
