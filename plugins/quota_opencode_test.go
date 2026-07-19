// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// callOpencodeFetch evaluates fetchers/opencode.js against the mocked env and
// returns a compact "label=used/limit;…" rendering of the fetch result.
func callOpencodeFetch(t *testing.T, env *quotaTestEnv) string {
	t.Helper()
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			var out = fetcher.fetch({ config: {}, session: {} });
			if (out.error) { return "error:" + out.error; }
			var parts = [];
			for (var i = 0; i < out.limits.length; i++) {
				var l = out.limits[i];
				parts.push(l.label + "=" + l.used + "/" + l.limit);
			}
			return "plan:" + (out.plan || "null") + "|costUnit:" + (out.costUnit || "null") + "|" + parts.join("|");
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	return v.String()
}

// seedOpencodeAuth stores a valid (unexpired) OAuth token so getToken returns
// it without a refresh round-trip.
func seedOpencodeAuth(env *quotaTestEnv) {
	env.storage.Set("opencode.access_token", "oc-tok")
	env.storage.Set("opencode.expires_at", "4102444800000") // year 2100 ms
}

// TestFetcherOpencode_SpendsAgainstPlanLimits drives the fetcher with the
// exact console API shape: org discovery, then per-window spend rows with
// costMicroCents summed against the $12/$30/$60 plan limits.
func TestFetcherOpencode_SpendsAgainstPlanLimits(t *testing.T) {
	env := newQuotaTestEnv(t)
	seedOpencodeAuth(env)
	env.respond("/api/orgs", 200, `[{"id":"org_1","name":"Personal"}]`)

	// Spend rows: $6 in the last hour (all windows), $15 three days ago
	// (weekly + monthly), $20 ten days ago (monthly only). costMicroCents:
	// $1 = 1e6.
	now := time.Now()
	row := func(hoursAgo float64, dollars int) string {
		ts := now.Add(-time.Duration(hoursAgo * float64(time.Hour))).UTC().Format(time.RFC3339)
		return fmt.Sprintf(`{"createdAt":%q,"costMicroCents":%d}`, ts, dollars*1000000)
	}
	env.respond("/api/usage/rows?scope=organization&range=24h", 200,
		`{"items":[`+row(1, 6)+`],"nextCursor":null}`)
	env.respond("/api/usage/rows?scope=organization&range=7d", 200,
		`{"items":[`+row(1, 6)+`,`+row(72, 15)+`],"nextCursor":null}`)
	env.respond("/api/usage/rows?scope=organization&range=30d", 200,
		`{"items":[`+row(1, 6)+`,`+row(72, 15)+`,`+row(240, 20)+`],"nextCursor":null}`)

	got := callOpencodeFetch(t, env)
	// $6 of $12 (5h), $21 of $30 (7d), $41 of $60 (30d), in cents.
	for _, want := range []string{
		"plan:OpenCode Go", "costUnit:cents",
		"Session (5h)=600/1200", "Weekly (7d)=2100/3000", "Monthly (30d)=4100/6000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("opencode fetch missing %q:\n%s", want, got)
		}
	}
}

// TestFetcherOpencode_UsageRowsNewestFirstStopsEarly verifies the scan stops
// at the first row older than the window (rows are newest-first), so stale
// spend never leaks into a shorter window.
func TestFetcherOpencode_UsageRowsNewestFirstStopsEarly(t *testing.T) {
	env := newQuotaTestEnv(t)
	seedOpencodeAuth(env)
	env.respond("/api/orgs", 200, `[{"id":"org_1"}]`)
	now := time.Now()
	row := func(hoursAgo float64, dollars int) string {
		ts := now.Add(-time.Duration(hoursAgo * float64(time.Hour))).UTC().Format(time.RFC3339)
		return fmt.Sprintf(`{"createdAt":%q,"costMicroCents":%d}`, ts, dollars*1000000)
	}
	// 24h range contains $5 two hours ago and $50 thirty hours ago; the 5h
	// window must only count the $5.
	env.respond("/api/usage/rows?scope=organization&range=24h", 200,
		`{"items":[`+row(2, 5)+`,`+row(30, 50)+`],"nextCursor":null}`)
	env.respond("/api/usage/rows?scope=organization&range=7d", 200, `{"items":[],"nextCursor":null}`)
	env.respond("/api/usage/rows?scope=organization&range=30d", 200, `{"items":[],"nextCursor":null}`)

	got := callOpencodeFetch(t, env)
	if !strings.Contains(got, "Session (5h)=500/1200") {
		t.Fatalf("5h window should count only recent spend: %s", got)
	}
}

// TestFetcherOpencode_PaginatesRows verifies cursor pagination accumulates
// across pages.
func TestFetcherOpencode_PaginatesRows(t *testing.T) {
	env := newQuotaTestEnv(t)
	seedOpencodeAuth(env)
	env.respond("/api/orgs", 200, `[{"id":"org_1"}]`)
	now := time.Now()
	row := func(dollars int) string {
		ts := now.Add(-time.Hour).UTC().Format(time.RFC3339)
		return fmt.Sprintf(`{"createdAt":%q,"costMicroCents":%d}`, ts, dollars*1000000)
	}
	// Registration order matters: the mock matches the FIRST responder whose
	// substring appears in the URL, and page-2 URLs carry both markers.
	env.respond("cursor=page2", 200, `{"items":[`+row(3)+`],"nextCursor":null}`)
	env.respond("range=24h", 200, `{"items":[`+row(2)+`],"nextCursor":"page2"}`)
	env.respond("range=7d", 200, `{"items":[],"nextCursor":null}`)
	env.respond("range=30d", 200, `{"items":[],"nextCursor":null}`)

	got := callOpencodeFetch(t, env)
	if !strings.Contains(got, "Session (5h)=500/1200") {
		t.Fatalf("paginated spend not accumulated: %s", got)
	}
}

// TestFetcherOpencode_NoAuth surfaces auth_required without a stored token.
func TestFetcherOpencode_NoAuth(t *testing.T) {
	env := newQuotaTestEnv(t)
	got := callOpencodeFetch(t, env)
	if got != "error:auth_required" {
		t.Fatalf("no token = %q, want error:auth_required", got)
	}
}

// TestFetcherOpencode_NoOrg surfaces a dedicated error when the token sees
// no org, instead of a misleading zero spend.
func TestFetcherOpencode_NoOrg(t *testing.T) {
	env := newQuotaTestEnv(t)
	seedOpencodeAuth(env)
	env.respond("/api/orgs", 200, `[]`)
	got := callOpencodeFetch(t, env)
	if got != "error:no_org" {
		t.Fatalf("no org = %q, want error:no_org", got)
	}
}

// TestQuota_OpencodeRendersCostRows pins the /quota rendering for spend
// windows: dollar amounts, not token counts.
func TestQuota_OpencodeRendersCostRows(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("opencode", map[string]any{"provider": "opencode"})
	seedOpencodeAuth(env)
	env.respond("/api/orgs", 200, `[{"id":"org_1"}]`)
	now := time.Now()
	ts := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	body := `{"items":[{"createdAt":` + fmt.Sprintf("%q", ts) + `,"costMicroCents":6000000}],"nextCursor":null}`
	env.respond("/api/usage/rows", 200, body)

	env.load(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "OpenCode (OpenCode Go)") {
		t.Fatalf("opencode plan row missing:\n%s", out)
	}
	if !strings.Contains(out, "$6.00/$12.00") {
		t.Fatalf("cost row should render dollars:\n%s", out)
	}
	if strings.Contains(out, "error: http_404") || strings.Contains(out, "http_404") {
		t.Fatalf("404 regression present:\n%s", out)
	}
}
