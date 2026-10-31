// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
	"time"
)

// TestQuota_OAuthLoginRefreshesCachedAuthRequired reproduces the login → quota
// sequence: the first quota render caches auth_required, then OAuth succeeds
// and the provider manager exposes the login-derived zcode JWT. The very next
// bare /quota must not replay the stale login instruction; it must refresh off
// the command path and emit the account's real balance.
func TestQuota_OAuthLoginRefreshesCachedAuthRequired(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg(""))
	env.setActiveProvider("zai-start-plan")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(
		startPlanLivePlan,
		`{"bucket_id":"b1","user_plan_id":"up1","total_units":100,"remaining_units":40,"expires_at":1900000000,"capabilities":["model:glm-5.3-flash"]}`,
	))
	env.load(t)

	before := env.callCommand("quota")
	if !strings.Contains(before, "auth required") {
		t.Fatalf("test setup did not cache the logged-out state:\n%s", before)
	}

	// This is the production post-login state: the auth bridge returns the
	// family token and pluginProvidersMap resolves the zcode JWT afresh.
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok", "zcodeJwt": "zjwt-test-token"})
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setDeviceMid("mid-abcdef")

	immediate := env.callCommand("quota")
	if strings.Contains(immediate, "auth required") || strings.Contains(immediate, "/login:zai:oauth") {
		t.Fatalf("bare /quota replayed stale auth state after OAuth login:\n%s", immediate)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := env.lastOutput()
		if strings.Contains(out, "Provider Quotas") {
			if strings.Contains(out, "auth required") || !strings.Contains(out, "60%") {
				t.Fatalf("refreshed post-login quota is incorrect:\n%s", out)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("post-login quota never emitted refreshed balance; last output:\n%s", env.lastOutput())
}

// TestQuota_UnconfiguredProvidersStayHidden pins the configured-provider
// contract across the table, JSON, auth status, and the fetch cache. A built-in
// fetcher is not configuration: its presence must neither render a row nor
// cause a provider request.
func TestQuota_UnconfiguredProvidersStayHidden(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk-ant"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"plan":{"name":"Pro"},"usage":{"session":{"used":42,"limit":100}}}`)
	env.load(t)
	env.warmCache(t)

	full := env.callCommand("quota")
	for _, unwanted := range []string{"Z.ai Start Plan", "BigModel Start Plan", "Z.ai", "Codex", "auth required"} {
		if strings.Contains(full, unwanted) {
			t.Errorf("unconfigured provider %q leaked into /quota:\n%s", unwanted, full)
		}
	}
	if !strings.Contains(full, "Anthropic") || !strings.Contains(full, "42%") {
		t.Errorf("configured provider missing from /quota:\n%s", full)
	}

	jsonOut := env.callCommand("quota", "json")
	for _, unwanted := range []string{`"zai-start-plan"`, `"bigmodel-start-plan"`, `"codex"`, `"zai"`} {
		if strings.Contains(jsonOut, unwanted) {
			t.Errorf("unconfigured provider %s leaked into /quota:json:\n%s", unwanted, jsonOut)
		}
	}
	if !strings.Contains(jsonOut, `"anthropic"`) {
		t.Errorf("configured provider missing from /quota:json:\n%s", jsonOut)
	}

	authStatus := env.callCommand("quota", "auth-status")
	if !strings.Contains(authStatus, "Anthropic") || strings.Contains(authStatus, "Start Plan") || strings.Contains(authStatus, "Codex") {
		t.Errorf("auth-status must contain configured providers only:\n%s", authStatus)
	}
	if cached := env.evalJSValue(t, `Object.keys(_cache).sort().join(",")`); cached != "anthropic,local" {
		t.Errorf("cache contains unconfigured providers: %s", cached)
	}
}

// TestQuota_ProviderRemovalPrunesCachedRows verifies live configuration is
// authoritative even after a provider was configured and fetched successfully.
func TestQuota_ProviderRemovalPrunesCachedRows(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk-ant"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "zai-key"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":38}]}}`)
	env.load(t)
	env.warmCache(t)
	if out := env.callCommand("quota"); !strings.Contains(out, "Z.ai (pro)") {
		t.Fatalf("test setup did not cache z.ai:\n%s", out)
	}

	env.removeProvider("z.ai")
	env.warmCache(t)
	out := env.callCommand("quota")
	if strings.Contains(out, "Z.ai (pro)") {
		t.Fatalf("removed provider remained in /quota:\n%s", out)
	}
	if !strings.Contains(out, "Anthropic") {
		t.Fatalf("remaining configured provider disappeared:\n%s", out)
	}
	if cached := env.evalJSValue(t, `Object.keys(_cache).sort().join(",")`); cached != "anthropic,local" {
		t.Errorf("removed provider remained cached: %s", cached)
	}
}
