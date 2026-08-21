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

// TestDefaultCacheRetention_Zai pins the cache-affinity default (bugs.md
// 2026-08-19): both Z.ai catalog entries declare long prompt-cache retention
// so BuildStreamOptions sends the session cache identity as the OpenAI-style
// prompt_cache_key without user configuration — the mitigation for the
// observed server-side prefix-cache evictions on content-keyed routing
// (z.ai live-probed HTTP 200 with prompt_cache_key + prompt_cache_retention).
func TestDefaultCacheRetention_Zai(t *testing.T) {
	for _, id := range []string{"zai", "zai-api"} {
		def := LookupProviderDefByID(id)
		if def == nil {
			t.Fatalf("%s not in catalog", id)
		}
		if def.DefaultCacheRetention != CacheRetentionLong {
			t.Errorf("%s DefaultCacheRetention = %q, want %q", id, def.DefaultCacheRetention, CacheRetentionLong)
		}
		if def.Compat.NoCacheRetention {
			t.Errorf("%s declares NoCacheRetention yet defaults to long retention", id)
		}
	}
}

// TestProviderCatalog_DefaultCacheRetentionLegal guards the catalog against
// typos and contradictions: every declared default must be a legal retention
// value, and a provider that opts out of long retention support must never
// declare it as its default.
func TestProviderCatalog_DefaultCacheRetentionLegal(t *testing.T) {
	legal := map[CacheRetention]bool{
		CacheRetentionNone: true, CacheRetentionShort: true, CacheRetentionLong: true,
	}
	for _, def := range ProviderCatalog() {
		if def.DefaultCacheRetention == "" {
			continue
		}
		if !legal[def.DefaultCacheRetention] {
			t.Errorf("%s DefaultCacheRetention = %q: not a legal retention value", def.ID, def.DefaultCacheRetention)
		}
		if def.Compat.NoCacheRetention && def.DefaultCacheRetention == CacheRetentionLong {
			t.Errorf("%s has NoCacheRetention but defaults to long retention", def.ID)
		}
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

// TestOpenAICodexCatalogEntry pins the Codex subscription provider identity:
// distinct Provider identity, codex-responses API, chatgpt backend base URL.
func TestOpenAICodexCatalogEntry(t *testing.T) {
	def := LookupProviderDefByID("openai-codex")
	if def == nil {
		t.Fatal("openai-codex not in catalog")
	}
	if def.Provider != ProviderOpenAICodex {
		t.Errorf("Provider = %q, want openai-codex", def.Provider)
	}
	if def.API != ApiOpenAICodexResponses {
		t.Errorf("API = %q, want openai-codex-responses", def.API)
	}
	if def.BaseURL != "https://chatgpt.com/backend-api" {
		t.Errorf("BaseURL = %q", def.BaseURL)
	}
	if got := MatchProviderByURL("https://chatgpt.com/backend-api/codex/responses"); got == nil || got.ID != "openai-codex" {
		t.Errorf("MatchProviderByURL = %v, want openai-codex", got)
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

// TestDefaultRetryCodes_MatchesDSHVocabulary pins the default transient code
// set to the dsh llm-retry DEFAULT_RETRYABLE_CODES vocabulary.
func TestDefaultRetryCodes_MatchesDSHVocabulary(t *testing.T) {
	want := []string{"EMPTY_RESPONSE", "RATE_LIMIT", "SERVER", "TIMEOUT", "TRANSPORT"}
	if len(DefaultRetryCodes) != len(want) {
		t.Fatalf("DefaultRetryCodes = %v, want %v", DefaultRetryCodes, want)
	}
	for i, code := range want {
		if DefaultRetryCodes[i] != code {
			t.Errorf("DefaultRetryCodes[%d] = %q, want %q", i, DefaultRetryCodes[i], code)
		}
	}
}

// TestDefaultMaxTokens_DeepSeek pins the P21 (DS2) catalog value: DeepSeek's
// adapter-owned default output cap is 256000 over a 1000000-token context
// window (dsh llm-deepseek DEFAULT_MAX_TOKENS / DEFAULT_CONTEXT_WINDOW,
// adapter.ts:91-93).
func TestDefaultMaxTokens_DeepSeek(t *testing.T) {
	def := LookupProviderDefByID("deepseek")
	if def == nil {
		t.Fatal("deepseek not in catalog")
	}
	if def.DefaultMaxTokens != 256000 {
		t.Errorf("deepseek DefaultMaxTokens = %d, want 256000", def.DefaultMaxTokens)
	}
}

// TestResolveRetryPolicy_NilUsesDefault verifies a nil configured policy
// resolves to the package default normal policy.
func TestResolveRetryPolicy_NilUsesDefault(t *testing.T) {
	p := ResolveRetryPolicy(nil, nil)
	if p == nil {
		t.Fatal("ResolveRetryPolicy(nil, nil) = nil")
	}
	if p.Mode != RetryModeNormal {
		t.Errorf("Mode = %q, want normal", p.Mode)
	}
	if p.MaxRetries != DefaultRetryPolicy.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", p.MaxRetries, DefaultRetryPolicy.MaxRetries)
	}
	// Must be a fresh copy: mutating it must not affect the package default.
	p.MaxRetries = 999
	if DefaultRetryPolicy.MaxRetries == 999 {
		t.Error("ResolveRetryPolicy returned a shared (non-cloned) default")
	}
}

// TestResolveRetryPolicy_ConfiguredOverrides verifies per-field override: the
// configured policy wins, catalog fills omissions, package default fills rest.
func TestResolveRetryPolicy_ConfiguredOverrides(t *testing.T) {
	catalog := &RetryPolicy{
		Mode:       RetryModeAlways,
		MaxRetries: 2,
		Backoff:    RetryBackoff{InitialDelay: 2 * time.Second, MaxDelay: 20 * time.Second, Jitter: 0.2},
		Codes:      []string{"SERVER"},
	}
	configured := &RetryPolicy{
		Mode:       RetryModeNormal,
		MaxRetries: 1,
	}
	p := ResolveRetryPolicy(configured, catalog)
	if p.Mode != RetryModeNormal {
		t.Errorf("Mode = %q, want configured normal", p.Mode)
	}
	if p.MaxRetries != 1 {
		t.Errorf("MaxRetries = %d, want configured 1", p.MaxRetries)
	}
	if p.Backoff.InitialDelay != 2*time.Second {
		t.Errorf("InitialDelay = %v, want catalog 2s", p.Backoff.InitialDelay)
	}
	if p.Backoff.MaxDelay != 20*time.Second {
		t.Errorf("MaxDelay = %v, want catalog 20s", p.Backoff.MaxDelay)
	}
	if len(p.Codes) != 1 || p.Codes[0] != "SERVER" {
		t.Errorf("Codes = %v, want catalog [SERVER]", p.Codes)
	}
}
