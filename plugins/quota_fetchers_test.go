// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
)

// readModuleSource reads a JS module from the quota plugin dir for in-VM
// evaluation, so fetchers and libs are unit-testable in isolation.
func readModuleSource(t *testing.T, modulePath string) string {
	t.Helper()
	data, err := readFileUnder(quotaPluginDir, modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	return string(data)
}

// --- format.js ---

func TestFormat_Tokens(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	unlock := lockVM()
	defer unlock()
	bridge.vm.RunString(formatJS + `
		__r = [tokens(0), tokens(500), tokens(142300), tokens(1250000)].join(",");
	`)
	if got := bridge.vm.Get("__r").String(); got != "0,500,142.3K,1.3M" {
		t.Fatalf("tokens = %q", got)
	}
}

func TestFormat_Bar(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	unlock := lockVM()
	defer unlock()
	bridge.vm.RunString(formatJS + `
		__r = bar(50, 10) + "|" + bar(0, 4) + "|" + bar(100, 4) + "|" + bar(150, 4);
	`)
	got := bridge.vm.Get("__r").String()
	if !strings.Contains(got, "█████░░░░░") {
		t.Fatalf("bar(50) wrong: %q", got)
	}
	if !strings.Contains(got, "░░░░|████|████") { // 0%, 100%, clamped 150%
		t.Fatalf("bar edges wrong: %q", got)
	}
}

func TestFormat_Pct(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	unlock := lockVM()
	defer unlock()
	bridge.vm.RunString(formatJS + `__r = [pct(42,100), pct(1,3), pct(10,0)].join(",");`)
	if got := bridge.vm.Get("__r").String(); got != "42,33,0" {
		t.Fatalf("pct = %q", got)
	}
}

func TestFormat_Humanize(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	unlock := lockVM()
	defer unlock()
	bridge.vm.RunString(formatJS + `
		__r = [humanize(3600000+48*60000), humanize(4*86400000+12*3600000), humanize(13*86400000), humanize(90000)].join("|");
	`)
	got := bridge.vm.Get("__r").String()
	if got != "1h 48m|4d 12h|13d|1m" {
		t.Fatalf("humanize = %q", got)
	}
}

// --- fetchers/local.js ---

func TestFetcherLocal_InfersFromSession(t *testing.T) {
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
			` + readModuleSource(t, "fetchers/local.js") + `
			var out = module.exports.fetch({ session: { input: 100000, output: 50000 } });
			return out.limits[0].used + ":" + module.exports.refreshInterval + ":" + module.exports.quotaEndpoint;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "150000:0:false" {
		t.Fatalf("local fetcher = %q", got)
	}
}

// --- fetchers/anthropic.js parsing ---

func TestFetcherAnthropic_ParsesWindows(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{
		"plan": {"name": "Pro"},
		"usage": {"session": {"used": 42, "limit": 100, "reset_at": "2099-01-01T00:00:00Z"},
		          "weekly": {"used": 30, "limit": 200}}
	}`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/anthropic.js");
			var ctx = { config: { apiKey: "sk" }, session: {} };
			var out = fetcher.fetch(ctx);
			return out.plan + "|" + out.limits.length + "|" + out.limits[0].label + ":" + out.limits[0].used + "/" + out.limits[0].limit;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "Pro|2|Session (5h):42/100" {
		t.Fatalf("anthropic parse = %q", got)
	}
}

func TestFetcherAnthropic_NoAPIKey(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, _ := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/anthropic.js");
			return fetcher.fetch({ config: {}, session: {} }).error;
		})()
	`)
	if got := v.String(); got != "no_api_key" {
		t.Fatalf("no_api_key = %q", got)
	}
}

// --- fetchers/opencode.js ---

// TestFetcherOpencode_ParsesRealUsageShape feeds the payload captured from the
// live endpoint (2026-08-17):
//
//	GET https://opencode.ai/zen/go/v1/usage  (Authorization: Bearer <key>)
//	{"usage":{"rolling":{"status":"ok","percent":0,"resetsAt":"…"},
//	          "weekly":{"status":"ok","percent":0,"resetsAt":"…"},
//	          "monthly":{"status":"ok","percent":85,"resetsAt":"…"}}}
//
// Regression for "opencode-go quota missing from /quota and the status bar":
// the first fetcher version invented a credits shape the real API does NOT
// return, so the mapper produced zero limits and the provider vanished.
func TestFetcherOpencode_ParsesRealUsageShape(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("opencode.ai/zen/go/v1/usage", 200, `{
		"usage": {
			"rolling": {"status":"ok","percent":12,"resetsAt":"2026-08-17T11:55:57.159Z"},
			"weekly":  {"status":"ok","percent":34,"resetsAt":"2026-08-24T00:00:00.159Z"},
			"monthly": {"status":"ok","percent":85,"resetsAt":"2026-09-05T15:47:55.159Z"}
		}
	}`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			var ctx = { config: { apiKey: "sk", baseUrl: "https://opencode.ai/zen/go/v1" }, session: {} };
			var out = fetcher.fetch(ctx);
			if (out.error) { return "ERR:" + out.error; }
			var parts = [];
			for (var i = 0; i < out.limits.length; i++) {
				var l = out.limits[i];
				parts.push(l.label + "=" + l.used + "/" + l.limit + "@" + l.periodMs + "->" + l.resetsAt);
			}
			return parts.join("|");
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	// Percent windows normalize to used/100 (z.ai idiom), shortest window
	// first: rolling (5h) → weekly (7d) → monthly (30d).
	want := "Session (5h)=12/100@18000000->2026-08-17T11:55:57.159Z" +
		"|Weekly=34/100@604800000->2026-08-24T00:00:00.159Z" +
		"|Monthly=85/100@2592000000->2026-09-05T15:47:55.159Z"
	if got := v.String(); got != want {
		t.Fatalf("opencode real-shape parse:\n got %q\nwant %q", got, want)
	}
}

// TestFetcherOpencode_ZenBaseRewritesToGo covers the dual-variant config: a
// provider whose base is the zen/v1 inference endpoint must still reach the
// usage API, which lives ONLY under /zen/go/v1 (the zen/v1/usage route serves
// an HTML 404 page — verified live 2026-08-17). Both variants share the same
// account and key, so the rewrite is safe.
func TestFetcherOpencode_ZenBaseRewritesToGo(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Only the go URL is mocked: a fetch against zen/v1/usage would 404.
	env.respond("opencode.ai/zen/go/v1/usage", 200, `{"usage":{"weekly":{"status":"ok","percent":7,"resetsAt":"2026-08-24T00:00:00.000Z"}}}`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			var ctx = { config: { apiKey: "sk", baseUrl: "https://opencode.ai/zen/v1" }, session: {} };
			var out = fetcher.fetch(ctx);
			if (out.error) { return "ERR:" + out.error; }
			return out.limits.length + ":" + out.limits[0].label + "=" + out.limits[0].used;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "1:Weekly=7" {
		t.Fatalf("zen base rewrite = %q", got)
	}
}

func TestFetcherOpencode_ParsesCredits(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("opencode.ai/zen/go/v1/usage", 200, `{
		"plan": "Pro",
		"data": { "balance": 42.5, "used": 7.5, "limit": 50 }
	}`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			var ctx = { config: { apiKey: "sk", baseUrl: "https://opencode.ai/zen/go/v1" }, session: {} };
			var out = fetcher.fetch(ctx);
			return out.plan + "|" + out.limits.length + "|" + out.limits[0].label + ":" + out.limits[0].used + "/" + out.limits[0].limit + "|" + out.costUnit;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	// used 7.5/limit 50 in dollars → 750/5000 cents.
	if got := v.String(); got != "Pro|1|Credits:750/5000|usd_credits" {
		t.Fatalf("opencode parse = %q", got)
	}
}

func TestFetcherOpencode_NoAPIKey(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, _ := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			return fetcher.fetch({ config: {}, session: {} }).error;
		})()
	`)
	if got := v.String(); got != "no_api_key" {
		t.Fatalf("no_api_key = %q", got)
	}
}

// TestFetcherOpencode_BalanceDerivation covers the two balance-only shapes: a
// known limit derives usage from the remaining balance (balance 40 / limit 50
// → 10 used), and a bare balance reports as a 0-used credit pool.
func TestFetcherOpencode_BalanceDerivation(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("opencode.ai/zen/go/v1/usage", 200, `{"data":{"balance":40,"limit":50}}`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var fetcher = globalThis.__require("fetchers/opencode.js");
			var ctx = { config: { apiKey: "sk" }, session: {} };
			var out = fetcher.fetch(ctx);
			return out.limits[0].label + ":" + out.limits[0].used + "/" + out.limits[0].limit;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "Credits:1000/5000" { // 10/50 dollars → cents
		t.Fatalf("balance-derived usage = %q", got)
	}
}

// --- oauth.js token refresh logic ---

func TestOAuth_RefreshWithinSkew(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Expired token + valid refresh → should call token endpoint and store new.
	env.storage.Set("kimi.access_token", "old-tok")
	env.storage.Set("kimi.refresh_token", "ref-tok")
	env.storage.Set("kimi.expires_at", "1000") // long past
	env.respond("moonshot.ai/oauth/device/token", 200, `{"access_token":"new-tok","expires_in":3600}`)

	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var oauth = require("` + "lib/oauth.js" + `");
			return oauth.getToken("kimi", { tokenUrl: "https://platform.moonshot.ai/oauth/device/token", clientId: "goa-plugin" });
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "new-tok" {
		t.Fatalf("refresh did not return new token: %q", got)
	}
	if stored := env.storage.Get("kimi.access_token"); stored != "new-tok" {
		t.Fatalf("new token not stored: %q", stored)
	}
}

func TestOAuth_NoRefreshWhenFresh(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.storage.Set("kimi.access_token", "good-tok")
	// expires_at far future (year 2100 in ms).
	env.storage.Set("kimi.expires_at", "4102444800000")
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, _ := bridge.vm.RunString(`
		(function() {
			var oauth = require("lib/oauth.js");
			return oauth.getToken("kimi", { tokenUrl: "https://x", clientId: "c" });
		})()
	`)
	if got := v.String(); got != "good-tok" {
		t.Fatalf("fresh token not reused: %q", got)
	}
}

// TestOAuth_AbsolutizeURL covers the relative-URL resolution table:
// absolute URLs pass through, root-relative and bare paths resolve against
// the issuer origin, and edge inputs degrade safely.
func TestOAuth_AbsolutizeURL(t *testing.T) {
	env := newQuotaTestEnv(t)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var oauth = require("lib/oauth.js");
			var a = oauth.absolutizeURL;
			return [
				a("/device?user_code=PCDM-TDQM", "https://console.opencode.ai/auth/device/code"),
				a("/device", "https://console.opencode.ai/auth/device/code"),
				a("device", "https://console.opencode.ai/auth/device/code"),
				a("https://other.example.com/activate", "https://console.opencode.ai/auth/device/code"),
				a("HTTP://UPPER.example.com/x", "https://console.opencode.ai/auth/device/code"),
				a("", "https://console.opencode.ai/auth/device/code"),
				a("/device", "")
			].join("\n");
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"https://console.opencode.ai/device?user_code=PCDM-TDQM",
		"https://console.opencode.ai/device",
		"https://console.opencode.ai/device",
		"https://other.example.com/activate",
		"HTTP://UPPER.example.com/x",
		"",
		"/device", // no base to resolve against — pass through
	}, "\n")
	if got := v.String(); got != want {
		t.Fatalf("absolutizeURL table mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
