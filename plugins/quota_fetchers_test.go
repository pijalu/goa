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
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var module = { exports: {} };
			var require = globalThis.__require;
			` + readModuleSource(t, "fetchers/anthropic.js") + `
			var ctx = { config: { apiKey: "sk" }, session: {} };
			var out = module.exports.fetch(ctx);
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
			var module = { exports: {} };
			var require = globalThis.__require;
			` + readModuleSource(t, "fetchers/anthropic.js") + `
			return module.exports.fetch({ config: {}, session: {} }).error;
		})()
	`)
	if got := v.String(); got != "no_api_key" {
		t.Fatalf("no_api_key = %q", got)
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
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()
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
