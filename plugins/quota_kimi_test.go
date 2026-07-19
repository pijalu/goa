// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// loadKimiFetcher evaluates fetchers/kimi.js in a fresh bridge and returns the
// result of calling fetch with the given ctx JSON. httpDo must already be
// mocked by the caller.
func callKimiFetch(t *testing.T, ctxJSON string) string {
	t.Helper()
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var module = { exports: {} };
			var require = globalThis.__require;
			` + readModuleSource(t, "fetchers/kimi.js") + `
			var out = module.exports.fetch(` + ctxJSON + `);
			if (out.error) { return "error:" + out.error; }
			var parts = [];
			for (var i = 0; i < out.limits.length; i++) {
				var l = out.limits[i];
				parts.push(l.label + "=" + l.used + "/" + l.limit);
			}
			return "plan:" + (out.plan || "null") + "|" + parts.join("|");
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	return v.String()
}

// TestFetcherKimi_ParsesRealAPIShape drives the fetcher with the exact payload
// returned by GET https://api.kimi.com/coding/v1/usages (verified live): string
// numerics, a limits[] window array, and a top-level usage object.
func TestFetcherKimi_ParsesRealAPIShape(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("api.kimi.com/coding/v1/usages", 200, `{
		"user": {"membership": {"level": "LEVEL_ADVANCED"}},
		"usage": {"limit": "100", "used": "21", "remaining": "79", "resetTime": "2099-07-24T07:41:33Z"},
		"limits": [
			{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			 "detail": {"limit": "100", "used": "4", "remaining": "96", "resetTime": "2099-07-19T09:41:33Z"}}
		]
	}`)
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()

	got := callKimiFetch(t, `{ config: { apiKey: "sk-kimi-x", endpoint: "https://api.kimi.com/coding/v1" }, session: {} }`)
	if !strings.Contains(got, "plan:Advanced") {
		t.Fatalf("plan label wrong: %q", got)
	}
	if !strings.Contains(got, "Session (5h)=4/100") {
		t.Fatalf("session window wrong: %q", got)
	}
	if !strings.Contains(got, "Weekly=21/100") {
		t.Fatalf("usage window wrong: %q", got)
	}
}

// TestFetcherKimi_NoAPIKey covers the unconfigured path: the footer must be
// able to surface auth state, and the fetcher must say so.
func TestFetcherKimi_NoAPIKey(t *testing.T) {
	got := callKimiFetch(t, `{ config: {}, session: {} }`)
	if got != "error:no_api_key" {
		t.Fatalf("no key = %q, want error:no_api_key", got)
	}
}

// TestFetcherKimi_AuthFailed surfaces a 401/403 as auth_required so the footer
// can show the "∇ auth" marker instead of silently falling back to "0 tok".
func TestFetcherKimi_AuthFailed(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("api.kimi.com", 401, `{"error":"unauthorized"}`)
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()

	got := callKimiFetch(t, `{ config: { apiKey: "bad", endpoint: "https://api.kimi.com/coding/v1" }, session: {} }`)
	if got != "error:auth_required" {
		t.Fatalf("401 = %q, want error:auth_required", got)
	}
}

// TestFetcherKimi_MonthlyWindow verifies long windows (>= 28d) are labeled
// Monthly so monthly-class plans show the right tag.
func TestFetcherKimi_MonthlyWindow(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("api.kimi.com", 200, `{
		"usage": {"limit": "1000", "used": "800", "resetTime": "2099-08-01T00:00:00Z"},
		"limits": [
			{"window": {"duration": 30, "timeUnit": "TIME_UNIT_DAY"},
			 "detail": {"limit": "1000", "used": "800", "resetTime": "2099-08-01T00:00:00Z"}}
		]
	}`)
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()

	got := callKimiFetch(t, `{ config: { apiKey: "k", endpoint: "https://api.kimi.com/coding/v1" }, session: {} }`)
	if !strings.Contains(got, "Monthly (30d)=800/1000") {
		t.Fatalf("monthly window wrong: %q", got)
	}
}

// TestQuota_KimiCodeConfigMatchesKimiFetcher is the regression for the reported
// bug: a user configured with provider id "kimi-code" (Goa's preset) must feed
// the kimi fetcher — providerConfigFor("kimi") has to find it, else the
// fetcher ran with an empty config and the footer showed a bare "0 tok".
func TestQuota_KimiCodeConfigMatchesKimiFetcher(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Mirror the user's real config entry (id kimi-code, provider kimi-code).
	env.setProvider("kimi-code", map[string]any{
		"id":       "kimi-code",
		"provider": "kimi-code",
		"apiKey":   "sk-kimi-test",
		"endpoint": "https://api.kimi.com/coding/v1",
	})
	env.respond("api.kimi.com/coding/v1/usages", 200, `{
		"user": {"membership": {"level": "LEVEL_ADVANCED"}},
		"usage": {"limit": "100", "used": "21", "remaining": "79", "resetTime": "2099-07-24T07:41:33Z"},
		"limits": [
			{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			 "detail": {"limit": "100", "used": "4", "remaining": "96", "resetTime": "2099-07-19T09:41:33Z"}}
		]
	}`)
	env.load(t)
	out := env.callCommand("quota", "kimi")
	if !strings.Contains(out, "Kimi") {
		t.Fatalf("/quota:kimi missing Kimi block: %q", out)
	}
	if !strings.Contains(out, "Advanced") {
		t.Fatalf("plan not shown: %q", out)
	}
	// Session (5h) percentage bar must appear — the core of the feature.
	if !strings.Contains(out, "Session") || !strings.Contains(out, "%") {
		t.Fatalf("session quota not rendered: %q", out)
	}
}

// TestQuota_SegmentShowsAuthMarker is the regression for "no message and show
// 0 tok": when the ACTIVE provider's quota fetch returns auth_required, the
// footer segment must flag the auth state instead of silently falling back to
// the local token counter. The provider name is not repeated — the footer
// already shows it in the model display.
func TestQuota_SegmentShowsAuthMarker(t *testing.T) {
	env := newQuotaTestEnv(t)
	// kimi-code is the active provider; the API rejects the key → auth_required.
	env.setProvider("kimi-code", map[string]any{"provider": "kimi-code", "apiKey": "bad", "endpoint": "https://api.kimi.com/coding/v1"})
	env.setActiveProvider("kimi-code")
	env.respond("api.kimi.com", 401, `{"error":"unauthorized"}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, "auth") {
		t.Fatalf("segment should flag auth state, got: %q", seg)
	}
	if !strings.Contains(seg, "∇") {
		t.Fatalf("segment should carry the auth marker: %q", seg)
	}
	if strings.Contains(seg, "0 tok") {
		t.Fatalf("segment must not silently fall back to local tokens when auth is required: %q", seg)
	}
}

// TestQuota_SegmentShowsWindowedPercent verifies the active provider renders
// the session/weekly percentages in the footer (the "[5h:42% / wk:30%]" form).
func TestQuota_SegmentShowsWindowedPercent(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("kimi-code", map[string]any{"provider": "kimi-code", "apiKey": "sk-kimi", "endpoint": "https://api.kimi.com/coding/v1"})
	env.setActiveProvider("kimi-code")
	env.respond("api.kimi.com/coding/v1/usages", 200, `{
		"user": {"membership": {"level": "LEVEL_ADVANCED"}},
		"usage": {"limit": "100", "used": "30", "resetTime": "2099-07-24T00:00:00Z"},
		"limits": [
			{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			 "detail": {"limit": "100", "used": "42", "resetTime": "2099-07-19T12:00:00Z"}}
		]
	}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, "[5h:42%") {
		t.Fatalf("segment missing session percent: %q", seg)
	}
	if !strings.Contains(seg, "30%]") {
		t.Fatalf("segment missing weekly percent: %q", seg)
	}
}

// TestQuota_SegmentTracksActiveProviderOnly pins the status-bar contract:
// with several providers holding quota data, the segment shows ONLY the
// active provider — no rotation, no other provider names.
func TestQuota_SegmentTracksActiveProviderOnly(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
	env.setActiveProvider("z.ai")
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, "38%") {
		t.Fatalf("segment should show the active provider (z.ai): %q", seg)
	}
	if strings.Contains(seg, "42%") || strings.Contains(seg, "Anthropic") {
		t.Fatalf("segment leaked the inactive provider: %q", seg)
	}
	// Bracketed compact form (ANSI-stripped: the segment may carry a color).
	stripped := ansi.Strip(seg)
	if !strings.HasPrefix(stripped, "[") || !strings.HasSuffix(stripped, "]") {
		t.Fatalf("segment should be bracketed: %q", stripped)
	}
}
