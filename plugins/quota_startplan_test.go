// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
	"time"
)

// The Start Plan tests pin the /quota visibility contract for the free GLM
// tier (zai-start-plan / bigmodel-start-plan): the provider rows must render
// balance data, per-family auth hints, business errors, and the pending /
// empty states — a Start Plan row must never silently vanish.

// startPlanBalanceEnvelope wraps plans/balances JSON into the billing/balance
// envelope shape. server_time is fixed in 2027 so relative expiry windows in
// the fixtures stay unambiguous.
func startPlanBalanceEnvelope(plans, balances string) string {
	return `{"code":200,"msg":"ok","data":{"server_time":1800000000,"plans":[` + plans + `],"balances":[` + balances + `]}}`
}

// startPlanLivePlan is an active plan with entitlements already effective
// (effective_at in the past relative to the envelope's server_time).
const startPlanLivePlan = `{"plan_id":"p1","user_plan_id":"up1","status":"active","name":"Start","starts_at":1700000000,"ends_at":1900000000,"entitlements":[{"effective_at":1700000000}]}`

// startPlanProviderCfg mirrors the production provider config for one Start
// Plan family: pluginProvidersMap resolves apiKey to the family zcode JWT and
// the endpoint points at the zcode plan runtime base.
func startPlanProviderCfg(apiKey string) map[string]any {
	return map[string]any{
		"provider": "zai-start-plan",
		"apiKey":   apiKey,
		"endpoint": "https://zcode.z.ai/api/v1/zcode-plan",
	}
}

// TestQuota_StartPlanAppearsInFullQuotaOutput is the Start Plan analogue of
// TestQuota_ZaiAppearsInFullQuotaOutput: with a logged-in family token, a
// device identity, and a healthy balance envelope, /quota must show the
// Z.ai Start Plan row with the per-model buckets (a total'd bucket as a
// percentage, a remaining-only bucket as its absolute), and the status
// segment must show the active provider's usage.
func TestQuota_StartPlanAppearsInFullQuotaOutput(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.setActiveProvider("zai-start-plan")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(
		startPlanLivePlan,
		`{"bucket_id":"b1","user_plan_id":"up1","total_units":100,"remaining_units":40,"expires_at":1900000000,"capabilities":["model:glm-5.3-flash"]},`+
			`{"bucket_id":"b2","user_plan_id":"up1","remaining_units":500,"capabilities":["model:glm-5.2"]}`,
	))
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai Start Plan") {
		t.Fatalf("/quota output must contain the Z.ai Start Plan row, got:\n%s", out)
	}
	if !strings.Contains(out, "glm-5.3-flash") {
		t.Fatalf("/quota output must name the bucket's servable model, got:\n%s", out)
	}
	// total 100, remaining 40 → 60 used → 60%.
	if !strings.Contains(out, "60%") {
		t.Fatalf("/quota output must show the 60%% usage of the totalled bucket, got:\n%s", out)
	}
	// remaining-only bucket renders its absolute count.
	if !strings.Contains(out, "glm-5.2") {
		t.Fatalf("/quota output must show the remaining-only bucket, got:\n%s", out)
	}

	// Wire contract: the family JWT rides as Bearer, the device identity as
	// X-Device-Mid — the balance endpoint rejects requests without it (400
	// code 3001), so a missing header is a local defect, not a server quirk.
	hdrs := env.seenAuthHeaders()
	if !strings.Contains(hdrs, "authorization: Bearer zjwt-test-token") {
		t.Fatalf("balance request must carry the family zcode JWT as Bearer, got headers:\n%s", hdrs)
	}
	if !strings.Contains(hdrs, "x-device-mid: mid-abcdef") {
		t.Fatalf("balance request must carry X-Device-Mid, got headers:\n%s", hdrs)
	}

	// Status segment tracks the active provider's usage.
	seg := env.renderSegment()
	if !strings.Contains(seg, "60%") {
		t.Fatalf("quota segment must show the active Start Plan usage, got: %q", seg)
	}
}

// TestQuota_StartPlanFamiliesAuthIndependently pins the reason the fetcher is
// a factory: a zai login must light up ONLY the zai-start-plan row. The
// bigmodel row stays auth-required with ITS OWN login hint — never a doomed
// request with the sibling's JWT.
func TestQuota_StartPlanFamiliesAuthIndependently(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-family-token"))
	env.setProvider("bigmodel-start-plan", map[string]any{
		"provider": "bigmodel-start-plan",
		"apiKey":   "bmjwt-family-token",
		"endpoint": "https://zcode.z.ai/api/v1/zcode-plan",
	})
	// Only the zai family is logged in.
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(
		startPlanLivePlan,
		`{"bucket_id":"b1","user_plan_id":"up1","total_units":80,"remaining_units":40,"expires_at":1900000000,"capabilities":["model:glm-5.3-flash"]}`,
	))
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "50%") {
		t.Fatalf("the logged-in zai family must render its balance row (50%%), got:\n%s", out)
	}
	if !strings.Contains(out, "BigModel Start Plan") {
		t.Fatalf("the logged-out bigmodel family must still appear, got:\n%s", out)
	}
	if !strings.Contains(out, "/login:bigmodel:oauth") {
		t.Fatalf("bigmodel row must carry its own family login hint, got:\n%s", out)
	}
	// Family isolation on the wire: only the zai JWT may be sent.
	if hdrs := env.seenAuthHeaders(); strings.Contains(hdrs, "bmjwt-family-token") {
		t.Fatalf("a logged-out family must never put its sibling's JWT on the wire, got headers:\n%s", hdrs)
	}
}

// TestQuota_BigModelStartPlanBalanceRow verifies the bigmodel family renders
// its own balance under its own name.
func TestQuota_BigModelStartPlanBalanceRow(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("bigmodel-start-plan", map[string]any{
		"provider": "bigmodel-start-plan",
		"apiKey":   "bmjwt-test-token",
		"endpoint": "https://zcode.z.ai/api/v1/zcode-plan",
	})
	env.setOAuthToken("bigmodel", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(
		startPlanLivePlan,
		`{"bucket_id":"b1","user_plan_id":"up1","total_units":100,"remaining_units":60,"expires_at":1900000000,"capabilities":["model:glm-5-turbo"]}`,
	))
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "BigModel Start Plan") {
		t.Fatalf("/quota output must contain the BigModel Start Plan row, got:\n%s", out)
	}
	if !strings.Contains(out, "glm-5-turbo") || !strings.Contains(out, "40%") {
		t.Fatalf("/quota output must show the glm-5-turbo bucket at 40%% used, got:\n%s", out)
	}
}

// TestQuota_StartPlanBusinessErrorSurfaces verifies a business-code envelope
// (code 1005 = quota exhausted) is surfaced verbatim in the row instead of
// looking like a network failure or vanishing.
func TestQuota_StartPlanBusinessErrorSurfaces(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.respond("billing/balance", 200, `{"code":1005,"msg":"token quota exhausted","data":null}`)
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai Start Plan") {
		t.Fatalf("the Start Plan row must not vanish on a business error, got:\n%s", out)
	}
	if !strings.Contains(out, "start_plan_error_1005") {
		t.Fatalf("/quota output must surface the business code verbatim, got:\n%s", out)
	}
}

// TestQuota_StartPlanDeviceMidRequired pins the fail-closed gate: without a
// device identity the fetcher must refuse to build a request the backend
// would answer with 400 code 3001 — the row explains itself and NO HTTP
// request leaves the process.
func TestQuota_StartPlanDeviceMidRequired(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	// Deliberately NO setDeviceMid: fresh-install state.
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai Start Plan") {
		t.Fatalf("the Start Plan row must not vanish without a device mid, got:\n%s", out)
	}
	if !strings.Contains(out, "start_plan_device_mid_required") {
		t.Fatalf("/quota output must explain the missing device identity, got:\n%s", out)
	}
	if hdrs := env.seenAuthHeaders(); hdrs != "" {
		t.Fatalf("a missing device mid must short-circuit before any HTTP request, got:\n%s", hdrs)
	}
}

// TestQuota_StartPlanPendingPlan covers the pending state: an active plan
// whose entitlements all start in the future is information, not an absence —
// one "Pending (starts <date>)" row with the UTC start date.
func TestQuota_StartPlanPendingPlan(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(
		`{"plan_id":"p2","user_plan_id":"up2","status":"active","name":"Start","starts_at":1900000000,"ends_at":1950000000,"entitlements":[{"effective_at":1900000000}]}`,
		``,
	))
	env.load(t)
	env.warmCache(t)

	wantDate := time.Unix(1900000000, 0).UTC().Format("2006-01-02")
	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai Start Plan") {
		t.Fatalf("the Start Plan row must not vanish while pending, got:\n%s", out)
	}
	if !strings.Contains(out, "Pending (starts "+wantDate+")") {
		t.Fatalf("/quota output must show the pending start date %s, got:\n%s", wantDate, out)
	}
}

// TestQuota_StartPlanNoActiveQuota covers the empty state: zero buckets and
// nothing pending renders an explicit "No active quota" row — never a
// vanished provider.
func TestQuota_StartPlanNoActiveQuota(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-start-plan", startPlanProviderCfg("zjwt-test-token"))
	env.setOAuthToken("zai", map[string]any{"accessToken": "oauth-tok"})
	env.setDeviceMid("mid-abcdef")
	env.respond("billing/balance", 200, startPlanBalanceEnvelope(startPlanLivePlan, ``))
	env.load(t)
	env.warmCache(t)

	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai Start Plan") {
		t.Fatalf("the Start Plan row must not vanish on an empty balance, got:\n%s", out)
	}
	if !strings.Contains(out, "No active quota") {
		t.Fatalf("/quota output must show the explicit empty-state row, got:\n%s", out)
	}
}

// TestQuota_StartPlanBalanceURLContract pins the balance route the fetcher
// builds, straight from the plugin VM's require cache: the runtime base
// (endpoint) + /billing/balance + the placeholder app_version, with a
// previously-copied full URL tolerated as the base.
func TestQuota_StartPlanBalanceURLContract(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)

	const wantRoute = "https://zcode.z.ai/api/v1/zcode-plan/billing/balance?app_version=0.0.0"
	for _, tc := range []struct {
		name string
		expr string
	}{
		{
			name: "endpoint base",
			expr: `require("./fetchers/zai-start-plan.js")._internal.balanceURL({endpoint: "https://zcode.z.ai/api/v1/zcode-plan"})`,
		},
		{
			name: "baseUrl base",
			expr: `require("./fetchers/zai-start-plan.js")._internal.balanceURL({baseUrl: "https://zcode.z.ai/api/v1/zcode-plan"})`,
		},
		{
			name: "default base",
			expr: `require("./fetchers/zai-start-plan.js")._internal.balanceURL({})`,
		},
		{
			name: "full URL as base",
			expr: `require("./fetchers/zai-start-plan.js")._internal.balanceURL({baseUrl: "https://zcode.z.ai/api/v1/zcode-plan/billing/balance"})`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := env.evalJSValue(t, tc.expr); got != wantRoute {
				t.Fatalf("balanceURL mismatch:\n got: %s\nwant: %s", got, wantRoute)
			}
		})
	}
}
