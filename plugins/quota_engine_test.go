// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"testing"
)

// callHTTPEngine runs lib/http-quota.js helpers in a fresh bridge with the
// mocked env's httpDo installed, evaluating the given JS expression.
func runEngineJS(t *testing.T, env *quotaTestEnv, expr string) string {
	t.Helper()
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	orig := httpDo
	httpDo = env.mockDo()
	defer func() { httpDo = orig }()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString("(function() { var hq = globalThis.__require(\"lib/http-quota.js\"); " + expr + " })()")
	if err != nil {
		t.Fatal(err)
	}
	return v.String()
}

// TestHTTPEngine_ErrorVocabulary pins the shared error mapping every provider
// relies on: 401/403 → auth_required, other non-200 → http_<status>,
// transport error → the goa error string, bad JSON → bad_response.
func TestHTTPEngine_ErrorVocabulary(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("/unauthorized", 401, `{}`)
	env.respond("/forbidden", 403, `{}`)
	env.respond("/missing", 404, `{}`)
	env.respond("/broken", 200, `not json`)
	env.respond("/ok", 200, `{"usage":{"session":{"used":5,"limit":100}}}`)

	got := runEngineJS(t, env, `
		var results = [];
		var urls = ["/unauthorized", "/forbidden", "/missing", "/broken"];
		for (var i = 0; i < urls.length; i++) {
			results.push(hq.getJSON("https://x.example" + urls[i], {}, function() { return { plan: null, limits: [] }; }).error);
		}
		var ok = hq.getJSON("https://x.example/ok", {}, function(b) { return { ok: true }; });
		results.push(ok.ok ? "ok" : "not-ok");
		return results.join(",");
	`)
	if got != "auth_required,auth_required,http_404,bad_response,ok" {
		t.Fatalf("error vocabulary = %q", got)
	}
}

// TestHTTPEngine_WindowedUsageMapper verifies the shared window mapper used by
// anthropic/zai/minimax: nested envelope unwrapping, plan extraction, reset
// passthrough, and periodMs tagging.
func TestHTTPEngine_WindowedUsageMapper(t *testing.T) {
	env := newQuotaTestEnv(t)
	got := runEngineJS(t, env, `
		var map = hq.windowedUsageMapper({
			session: { label: "Session (5h)", periodMs: 18000000 },
			weekly: { label: "Weekly", periodMs: 604800000 }
		});
		var out = map({ plan: { name: "Pro" }, usage: {
			session: { used: 42, limit: 100, reset_at: "2099-01-01T00:00:00Z" },
			weekly: { used: 30, limit: 200 }
		}});
		return out.plan + "|" + out.limits.length + "|" +
			out.limits[0].label + "=" + out.limits[0].used + "/" + out.limits[0].limit + "@" + out.limits[0].resetsAt + "+" + out.limits[0].periodMs + "|" +
			out.limits[1].label + "=" + out.limits[1].used + "/" + out.limits[1].limit;
	`)
	if got != "Pro|2|Session (5h)=42/100@2099-01-01T00:00:00Z+18000000|Weekly=30/200" {
		t.Fatalf("windowed map = %q", got)
	}
}

// TestHTTPEngine_DataEnvelopeUnwrap covers the body.data variant (zai style).
func TestHTTPEngine_DataEnvelopeUnwrap(t *testing.T) {
	env := newQuotaTestEnv(t)
	got := runEngineJS(t, env, `
		var map = hq.windowedUsageMapper({ session: { label: "S", periodMs: 1 } });
		var out = map({ data: { plan: "Free", session: { used: 7, limit: 50, reset_at: null } } });
		return out.plan + "|" + out.limits[0].used + "/" + out.limits[0].limit;
	`)
	if got != "Free|7/50" {
		t.Fatalf("data envelope = %q", got)
	}
}

// TestHTTPEngine_NewProviderByDescriptorOnly proves the Open/Closed claim:
// onboarding a new provider requires only a descriptor — no changes to the
// engine or the plugin skeleton.
func TestHTTPEngine_NewProviderByDescriptorOnly(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("api.example.com/quota", 200, `{"usage":{"session":{"used":9,"limit":10}}}`)
	got := runEngineJS(t, env, `
		var desc = {
			auth: hq.apiKeyAuth().auth,
			authError: "no_api_key",
			url: function(ctx) { return ctx.config.baseUrl + "/quota"; },
			headers: hq.bearerHeaders,
			map: hq.windowedUsageMapper({ session: { label: "Session", periodMs: 3600000 } })
		};
		var out = hq.runFetch(desc, { config: { apiKey: "k", baseUrl: "https://api.example.com" } });
		var noKey = hq.runFetch(desc, { config: { baseUrl: "https://api.example.com" } });
		return out.limits[0].used + "/" + out.limits[0].limit + "|" + noKey.error;
	`)
	if got != "9/10|no_api_key" {
		t.Fatalf("descriptor-only provider = %q", got)
	}
}
