// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nowMs is the current wall clock in ms epoch, for building throttling
// boundaries that are in the future.
func nowMs() int64 { return time.Now().UnixMilli() }

// itoa renders an int64 boundary for embedding in a JSON fixture.
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// zaiCodingPlanModule evaluates an expression with `cp` bound to the real
// fetchers/zai-coding-plan.js module inside the loaded plugin VM.
func zaiCodingPlanModule(t *testing.T, e *quotaTestEnv, expr string) string {
	t.Helper()
	return e.evalJSString(t, `(function(){
		var cp = require("fetchers/zai-coding-plan.js");
		return `+expr+`;
	})()`)
}

// zaiCodingPlanObj is zaiCodingPlanModule for expressions returning an object,
// exported as a Go map.
func zaiCodingPlanObj(t *testing.T, e *quotaTestEnv, expr string) map[string]any {
	t.Helper()
	return e.evalJSONObj(t, `(function(){
		var cp = require("fetchers/zai-coding-plan.js");
		return `+expr+`;
	})()`)
}

// zaiMonitorBody is a canned monitor quota response so the consumption half of
// the z.ai fetcher succeeds (the reset status rides along with it).
const zaiMonitorBody = `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":41,"nextResetTime":1784656400096}]}}`

// zaiStatusBody is the coding-plan status envelope: two 5-hour resets, one
// week reset, both history rows, unread marker set.
const zaiStatusBody = `{"code":0,"data":{"available_five_hour_resets":[{"expire_at":1784700000000},{"expire_at":1784600000000}],"available_week_resets":[{"expire_at":1785200000000}],"latest_five_hour_reset_history":{"used_at":1784500000000},"latest_week_reset_history":null,"has_unread_history":true}}`

// wireZaiCodingPlan configures a z.ai provider carrying the DUAL credentials
// the reset API needs (config zcodeJwt + a Goa-managed family OAuth token) and
// registers the monitor + status responses. Everything must be in place BEFORE
// load: the plugin primes its cache at load time.
func wireZaiCodingPlan(t *testing.T, e *quotaTestEnv) {
	t.Helper()
	e.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k", "zcodeJwt": "zjwt-1",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	e.setActiveProvider("z.ai")
	e.setOAuthToken("zai", map[string]any{"accessToken": "zai-oauth-tok"})
	e.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	e.respond("coding-plan/reset/status", 200, zaiStatusBody)
}

// zaiCtx is the fetcher ctx expression used by the direct-module tests: the
// live provider config plus an empty session.
const zaiCtx = `{config: providerConfigFor("zai"), session: {}}`

// --- status mapping / surfacing ---------------------------------------------

// TestQuotaZaiCodingPlan_StatusMapsAvailableAndHistory pins the status mapping:
// available_five_hour_resets / available_week_resets keep their expiries (ms
// epoch), latest_*_history become used-at stamps, has_unread_history is a
// boolean, and entries without a positive expire_at are dropped instead of
// rendering as "expires now".
func TestQuotaZaiCodingPlan_StatusMapsAvailableAndHistory(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.load(t)

	got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if n, _ := got["availableCount"].(float64); n != 3 {
		t.Fatalf("availableCount = %v, want 3 (raw: %v)", got["availableCount"], got)
	}
	five, _ := got["availableFiveHour"].([]any)
	if len(five) != 2 {
		t.Fatalf("availableFiveHour = %v, want 2 entries", five)
	}
	first, _ := five[0].(map[string]any)
	if first["expireAtMs"] != float64(1784700000000) {
		t.Fatalf("expireAtMs = %v, want 1784700000000", first["expireAtMs"])
	}
	week, _ := got["availableWeek"].([]any)
	if len(week) != 1 {
		t.Fatalf("availableWeek = %v, want 1 entry", week)
	}
	if got["latestFiveHourUsedAtMs"] != float64(1784500000000) {
		t.Fatalf("latestFiveHourUsedAtMs = %v", got["latestFiveHourUsedAtMs"])
	}
	if got["latestWeekUsedAtMs"] != float64(0) {
		t.Fatalf("latestWeekUsedAtMs = %v, want 0 for a null history", got["latestWeekUsedAtMs"])
	}
	if got["hasUnreadHistory"] != true {
		t.Fatalf("hasUnreadHistory = %v, want true", got["hasUnreadHistory"])
	}

	// Degenerate rows never reach the UI: no positive expiry, non-array field.
	raw := zaiCodingPlanObj(t, env, `cp.mapStatus({
		available_five_hour_resets: [{expire_at: 0}, {expire_at: 5}],
		available_week_resets: "nope",
		has_unread_history: false
	})`)
	degraded, _ := raw["availableFiveHour"].([]any)
	if len(degraded) != 1 {
		t.Fatalf("expire_at 0 must be dropped, got %v", degraded)
	}
	if raw["availableCount"] != float64(1) {
		t.Fatalf("availableCount = %v, want 1", raw["availableCount"])
	}
}

// TestQuotaZaiCodingPlan_MissingCredentialsSurfaceOnRead is the READ half of
// the missing-resets bug (the claim half is pinned by
// TestQuotaZaiCodingPlan_MissingCredentialsExplainThemselves below).
//
// An account holding resets showed NOTHING: the fetcher's status failed with
// coding_plan_reset_zcode_jwt_required, zai.js dropped the failed status, and
// both renderers returned "". The user could not tell "no resets" from "resets
// hidden behind an unconfigured credential" — and /quota is the surface they
// actually look at. A failed status must surface its reason and the exact
// config that fixes it.
func TestQuotaZaiCodingPlan_MissingCredentialsSurfaceOnRead(t *testing.T) {
	env := newQuotaTestEnv(t)
	// A z.ai provider with an API key but NO reset credentials: the API key
	// authenticates the monitor (consumption) half, never the reset surface.
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)

	env.load(t)
	resets := env.callCommand("quota", "resets")
	if !strings.Contains(resets, "Coding Plan Resets") {
		t.Fatalf("/quota:resets must still show the section header so the gap is legible:\n%s", resets)
	}
	for _, want := range []string{"zcode_jwt_required", "zcodeJwt"} {
		if !strings.Contains(resets, want) {
			t.Errorf("/quota:resets missing %q (the user cannot act on a bare failure):\n%s", want, resets)
		}
	}

	// The bare /quota table carries the same diagnosis, so the row is not
	// simply absent from the summary the user actually looks at.
	env.callCommand("quota", "refresh")
	full := env.callCommand("quota")
	if !strings.Contains(full, "zcode_jwt_required") {
		t.Fatalf("/quota must explain the missing reset credential:\n%s", full)
	}
	// The consumption half is unaffected: a reset-API failure never takes down
	// the primary quota row.
	if !strings.Contains(full, "Z.ai") {
		t.Fatalf("/quota lost the z.ai consumption row:\n%s", full)
	}
}

// TestQuotaZaiCodingPlan_StatusBarStaysQuietWithoutCredentials verifies the
// status-bar segment does NOT advertise resets it could not fetch: the count
// suffix appears only for a real, successful status.
func TestQuotaZaiCodingPlan_StatusBarStaysQuietWithoutCredentials(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)

	env.load(t)
	seg := env.renderSegment()
	if strings.Contains(seg, "↻") {
		t.Fatalf("status segment must not claim resets it could not fetch: %q", seg)
	}
}

// TestQuotaZaiCodingPlan_MissingFamilyTokenExplainsItself pins that the OTHER
// half of the dual credential names itself: with zcodeJwt present but no family
// OAuth token, the message must blame the MaaS credential, not the JWT.
func TestQuotaZaiCodingPlan_MissingFamilyTokenExplainsItself(t *testing.T) {
	env := newQuotaTestEnv(t)
	// zcodeJwt present, family OAuth token absent → the MaaS credential is the
	// missing one, and the message must say so.
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k", "zcodeJwt": "zjwt-1",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)

	env.load(t)
	resets := env.callCommand("quota", "resets")
	if !strings.Contains(resets, "maas_jwt_required") {
		t.Fatalf("/quota:resets must name the missing MaaS credential:\n%s", resets)
	}
}

// TestQuotaZaiCodingPlan_UnconfiguredProviderRendersNothingExtra is the
// negative control for the two tests above: with no z.ai provider at all there
// is nothing to diagnose, so no reset section may appear. Without this the
// explanation would fire for users who never configured z.ai.
func TestQuotaZaiCodingPlan_UnconfiguredProviderRendersNothingExtra(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.load(t)
	if out := env.callCommand("quota", "resets"); strings.Contains(out, "Coding Plan Resets") {
		t.Fatalf("an unconfigured z.ai must not render a reset section:\n%s", out)
	}
}

// TestQuotaZaiCodingPlan_StatusSurfacesInResetsAndQuota is the user-visible
// half of bugs.md #7: /quota:resets lists the available 5-hour / week resets
// with their history, and the bare /quota table carries the count row.
func TestQuotaZaiCodingPlan_StatusSurfacesInResetsAndQuota(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.load(t)

	resets := env.callCommand("quota", "resets")
	for _, want := range []string{"z.ai Coding Plan Resets", "| 5-hour | 2 |", "| Week | 1 |", "Unread reset history"} {
		if !strings.Contains(resets, want) {
			t.Fatalf("/quota:resets missing %q:\n%s", want, resets)
		}
	}

	env.callCommand("quota", "refresh")
	full := env.callCommand("quota")
	if !strings.Contains(full, "Coding Plan Resets") || !strings.Contains(full, "3 available (2 × 5h · 1 × week)") {
		t.Fatalf("/quota must carry the Coding Plan reset row:\n%s", full)
	}
}

// TestQuotaZaiCodingPlan_SegmentShowsResetCount pins the status-bar contract:
// the available reset count rides along with the active z.ai quota, and stays
// out of the way when there is nothing available.
func TestQuotaZaiCodingPlan_SegmentShowsResetCount(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.load(t)
	env.callCommand("quota", "refresh")

	if seg := env.renderSegment(); !strings.Contains(seg, "+3↻") {
		t.Fatalf("segment must show the 3 available Coding Plan resets: %q", seg)
	}

	// No resets available → no suffix (a count of zero is noise).
	env.respond("coding-plan/reset/status", 200,
		`{"code":0,"data":{"available_five_hour_resets":[],"available_week_resets":[],"has_unread_history":false}}`)
	env.callCommand("quota", "refresh")
	if seg := env.renderSegment(); strings.Contains(seg, "↻") {
		t.Fatalf("segment must not show a reset count when none are available: %q", seg)
	}
}

// --- throttling: 3301 next_try_at and HTTP 429 -------------------------------

// TestQuotaZaiCodingPlan_Throttle3301PreservesNextTryAt pins the throttling
// contract: business code 3301 hands us data.next_try_at, which is passed
// through VERBATIM and persisted as the retry boundary. A follow-up status
// call inside the cooldown answers from storage — no second request, so the
// client cannot re-poll into the rate limit it was just told to respect.
func TestQuotaZaiCodingPlan_Throttle3301PreservesNextTryAt(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	boundary := int64(1784600000000)
	env.respond("coding-plan/reset/status", 200,
		`{"code":3301,"msg":"throttled","data":{"granted":false,"next_try_at":`+itoa(boundary)+`}}`)
	env.load(t)
	cap := startCapture(t, env)

	got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if got["throttled"] != true {
		t.Fatalf("3301 must map to a throttled state, got %v", got)
	}
	if int64(got["nextTryAt"].(float64)) != boundary {
		t.Fatalf("nextTryAt = %v, want the server value %d verbatim", got["nextTryAt"], boundary)
	}
	until, _ := got["cooldownUntil"].(float64)
	if until < float64(boundary) {
		t.Fatalf("cooldownUntil = %v must be at least the server boundary %d", until, boundary)
	}
	if got["source"] != "next_try_at" {
		t.Fatalf("source = %v, want next_try_at", got["source"])
	}

	// The cooldown is honored without a request: a second status call is
	// answered from storage.
	before := cap.getsTo("coding-plan/reset/status")
	again := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if again["throttled"] != true || int64(again["nextTryAt"].(float64)) != boundary {
		t.Fatalf("cooldown state must survive the call: %v", again)
	}
	if after := cap.getsTo("coding-plan/reset/status"); after != before {
		t.Fatalf("status must not re-request inside a cooldown (GETs %d → %d)", before, after)
	}
}

// TestQuotaZaiCodingPlan_HTTP429MapsToSameCooldown pins the bare-429 path: the
// backend throttles without an envelope when the call races the grant lock, so
// the client must synthesize the same cooldown state from a fallback duration
// rather than treat it as an unknown error.
func TestQuotaZaiCodingPlan_HTTP429MapsToSameCooldown(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/status", 429, `too many requests`)
	env.load(t)
	cap := startCapture(t, env)

	got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if got["throttled"] != true {
		t.Fatalf("HTTP 429 must map to a throttled state, got %v", got)
	}
	if got["nextTryAt"] != float64(0) {
		t.Fatalf("nextTryAt = %v, want 0 (a bare 429 carries no server boundary)", got["nextTryAt"])
	}
	if got["source"] != "429" {
		t.Fatalf("source = %v, want 429", got["source"])
	}
	until, _ := got["cooldownUntil"].(float64)
	if until-float64(nowMs()) < 9*60*1000 {
		t.Fatalf("cooldownUntil = %v, want the fallback cooldown (~10m)", until)
	}

	before := cap.getsTo("coding-plan/reset/status")
	zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if after := cap.getsTo("coding-plan/reset/status"); after != before {
		t.Fatalf("429 cooldown must be honored without a new request (GETs %d → %d)", before, after)
	}
}

// TestQuotaZaiCodingPlan_ThrottledStatusRendersInCommands proves the persisted
// boundary reaches the user: /quota:resets reports the throttle instead of a
// table, and /quota:reset refuses to send a call the server just declined.
func TestQuotaZaiCodingPlan_ThrottledStatusRendersInCommands(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/status", 200,
		`{"code":3301,"data":{"granted":false,"next_try_at":`+itoa(nowMs()+30*60*1000)+`}}`)
	env.load(t)

	resets := env.callCommand("quota", "resets")
	if !strings.Contains(resets, "Throttled by the API") || !strings.Contains(resets, "next attempt") {
		t.Fatalf("/quota:resets must report the throttle: %q", resets)
	}
	reset := env.callCommand("quota", "reset")
	if !strings.Contains(reset, "throttled") {
		t.Fatalf("/quota:reset must refuse while throttled: %q", reset)
	}
}

// --- idempotency key validation ----------------------------------------------

// TestQuotaZaiCodingPlan_InvalidIdempotencyKeySendsNothing pins the mandatory
// ≤64-char idempotency_key contract on BOTH mutating calls: a bad key fails
// locally and no request goes out (a rejected call would waste a rate-limit
// slot on an account the server is already throttling).
func TestQuotaZaiCodingPlan_InvalidIdempotencyKeySendsNothing(t *testing.T) {
	tooLong := strings.Repeat("k", 65)
	for _, tc := range []struct {
		name string
		call string
	}{
		{"opportunity empty key", `cp.requestOpportunity(` + zaiCtx + `, "  ")`},
		{"opportunity oversized key", `cp.requestOpportunity(` + zaiCtx + `, "` + tooLong + `")`},
		{"use empty key", `cp.useReset(` + zaiCtx + `, "FIVE_HOUR", "")`},
		{"use oversized key", `cp.useReset(` + zaiCtx + `, "WEEK", "` + tooLong + `")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaTestEnv(t)
			wireZaiCodingPlan(t, env)
			env.load(t)
			cap := startCapture(t, env)

			got := zaiCodingPlanObj(t, env, tc.call)
			if got["error"] != "coding_plan_reset_invalid_idempotency_key" {
				t.Fatalf("error = %v, want coding_plan_reset_invalid_idempotency_key", got["error"])
			}
			if n := len(cap.postsTo("coding-plan/reset")); n != 0 {
				t.Fatalf("an invalid key must send no request, got %d POSTs", n)
			}
		})
	}
}

// TestQuotaZaiCodingPlan_ValidKeyAndTypeAreSent pins the happy-path wire
// contract of both mutating calls: exact URLs, an idempotency_key on the body,
// reset_type on the use call, and the dual credentials in the headers.
func TestQuotaZaiCodingPlan_ValidKeyAndTypeAreSent(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/opportunity", 200, `{"code":0,"data":{"granted":true}}`)
	env.respond("coding-plan/reset/use", 200, `{"code":0,"data":{"used":true}}`)
	env.load(t)
	cap := startCapture(t, env)

	if got := zaiCodingPlanObj(t, env, `cp.requestOpportunity(`+zaiCtx+`, "claim-key")`); got["granted"] != true {
		t.Fatalf("requestOpportunity = %v, want granted", got)
	}
	claim := cap.postsTo("coding-plan/reset/opportunity")
	if len(claim) != 1 {
		t.Fatalf("expected 1 opportunity POST, got %d", len(claim))
	}
	if claim[0].URL != "https://zcode.z.ai/api/v1/coding-plan/reset/opportunity" {
		t.Fatalf("opportunity URL = %q", claim[0].URL)
	}
	assertCodingPlanBody(t, claim[0], map[string]any{"idempotency_key": "claim-key"})
	assertCodingPlanHeaders(t, claim[0], "zai-oauth-tok", true)

	if got := zaiCodingPlanModule(t, env, `String(JSON.stringify(cp.useReset(`+zaiCtx+`, "week", "use-key")))`); got != `{"used":true,"resetType":"WEEK"}` {
		t.Fatalf("useReset returned %s", got)
	}
	use := cap.postsTo("coding-plan/reset/use")
	if len(use) != 1 {
		t.Fatalf("expected 1 use POST, got %d", len(use))
	}
	assertCodingPlanBody(t, use[0], map[string]any{"idempotency_key": "use-key", "reset_type": "WEEK"})
	assertCodingPlanHeaders(t, use[0], "zai-oauth-tok", true)

	// An unknown reset type is rejected before any request.
	if got := zaiCodingPlanObj(t, env, `cp.useReset(`+zaiCtx+`, "decade", "use-key")`); got["error"] != "coding_plan_reset_invalid_reset_type" {
		t.Fatalf("unknown reset type must fail locally, got %v", got)
	}
	if n := len(cap.postsTo("coding-plan/reset/use")); n != 1 {
		t.Fatalf("rejected reset type must not POST, got %d use POSTs", n)
	}
}

// TestQuotaZaiCodingPlan_UseRetainsIdempotencyKeyAcrossRetry proves the consume
// key is retained until a terminal outcome: a failed attempt keeps the same key
// (the server dedupes a double-consume), and a successful one clears it so the
// next reset mints a fresh id.
func TestQuotaZaiCodingPlan_UseRetainsIdempotencyKeyAcrossRetry(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/use", 500, `boom`)
	env.load(t)
	cap := startCapture(t, env)

	if got := zaiCodingPlanObj(t, env, `cp.useReset(`+zaiCtx+`, "FIVE_HOUR")`); got["error"] != "http_500" {
		t.Fatalf("first attempt should surface the transport error, got %v", got)
	}
	env.respond("coding-plan/reset/use", 200, `{"code":0,"data":{"used":true}}`)
	if got := zaiCodingPlanObj(t, env, `cp.useReset(`+zaiCtx+`, "FIVE_HOUR")`); got["used"] != true {
		t.Fatalf("retry should succeed, got %v", got)
	}
	keys := codingPlanKeys(t, cap.postsTo("coding-plan/reset/use"))
	if len(keys) != 2 {
		t.Fatalf("expected 2 use POSTs, got %d", len(keys))
	}
	if keys[0] != keys[1] {
		t.Fatalf("the retry MUST reuse the retained idempotency_key: %q vs %q", keys[0], keys[1])
	}
	env.respond("coding-plan/reset/use", 200, `{"code":0,"data":{"used":true}}`)
	zaiCodingPlanObj(t, env, `cp.useReset(`+zaiCtx+`, "FIVE_HOUR")`)
	keys = codingPlanKeys(t, cap.postsTo("coding-plan/reset/use"))
	if keys[2] == keys[1] {
		t.Fatalf("a terminal outcome must clear the key so the next use mints a new one: %q", keys[2])
	}
	if !uuidV4RE.MatchString(keys[2]) {
		t.Fatalf("minted key %q is not a UUIDv4", keys[2])
	}
}

// --- dual auth / no cross-family fallback ------------------------------------

// TestQuotaZaiCodingPlan_NoCrossFamilyCredentialFallback pins the credential
// contract: the zcode JWT and the FAMILY's OAuth token are both mandatory, the
// family token is sent RAW, and neither credential may be substituted by the
// other family's token or by the inference API key. Each family reads only its
// own token key.
func TestQuotaZaiCodingPlan_NoCrossFamilyCredentialFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		identity   string
		endpoint   string
		family     string
		haveFamily string
		jwt        string
	}{
		{
			name: "zai family without its oauth token", identity: "zai",
			endpoint: "https://api.z.ai/api/coding/paas/v4", family: "zai",
			haveFamily: "bigmodel", jwt: "zjwt-1",
		},
		{
			// The CN coding plan is the same provider identity on a different
			// host: the family follows the endpoint, and a z.ai token must not
			// stand in for the bigmodel one.
			name: "bigmodel family without its oauth token", identity: "zai",
			endpoint: "https://open.bigmodel.cn/api/coding/paas/v4", family: "bigmodel",
			haveFamily: "zai", jwt: "zjwt-1",
		},
		{
			name: "missing zcode jwt", identity: "zai",
			endpoint: "https://api.z.ai/api/coding/paas/v4", family: "zai",
			haveFamily: "zai", jwt: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaTestEnv(t)
			cfg := map[string]any{"provider": tc.identity, "apiKey": "k", "endpoint": tc.endpoint}
			if tc.jwt != "" {
				cfg["zcodeJwt"] = tc.jwt
			}
			env.setProvider("z.ai", cfg)
			env.setActiveProvider("z.ai")
			// The OTHER family is authenticated: a cross-family fallback would
			// find this token and (wrongly) proceed.
			env.setOAuthToken(tc.haveFamily, map[string]any{"accessToken": tc.haveFamily + "-tok"})
			env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
			env.respond("bigmodel.cn/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
			env.load(t)
			cap := startCapture(t, env)

			got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
			if got["error"] != "auth_required" {
				t.Fatalf("error = %v, want auth_required (no cross-family fallback)", got["error"])
			}
			if reason, _ := got["reason"].(string); reason == "" {
				t.Fatalf("auth failure must name the missing credential, got %v", got)
			}
			if n := cap.getsTo("coding-plan/reset"); n != 0 {
				t.Fatalf("no reset request may be sent without both credentials, got %d", n)
			}
		})
	}
}

// TestQuotaZaiCodingPlan_BigmodelFamilyUsesItsOwnToken proves the positive
// half: a CN (bigmodel-host) account authenticates with the bigmodel token,
// while the zai token (also present in the test env) is never sent.
func TestQuotaZaiCodingPlan_BigmodelFamilyUsesItsOwnToken(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k", "zcodeJwt": "zjwt-1",
		"endpoint": "https://open.bigmodel.cn/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	env.setOAuthToken("bigmodel", map[string]any{"accessToken": "bigmodel-tok"})
	env.setOAuthToken("zai", map[string]any{"accessToken": "zai-tok"})
	env.respond("bigmodel.cn/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.respond("coding-plan/reset/status", 200, zaiStatusBody)
	env.load(t)
	cap := startCapture(t, env)

	got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if got["availableCount"] != float64(3) {
		t.Fatalf("bigmodel family should map the status, got %v", got)
	}
	gets := cap.getsTo("coding-plan/reset/status")
	if gets == 0 {
		t.Fatal("expected a status request")
	}
	for _, r := range cap.all() {
		if !strings.Contains(r.URL, "coding-plan/reset") {
			continue
		}
		if r.Headers["X-Bigmodel-Authorization"] != "bigmodel-tok" {
			t.Fatalf("family token = %q, want bigmodel-tok (never the zai one)", r.Headers["X-Bigmodel-Authorization"])
		}
	}
}

// TestQuotaZaiCodingPlan_HistoryReadOmitsTargetScope pins the header contract of
// the history-read call: same dual credentials, but no plan scope — the read
// cursor is per user, not per scope.
func TestQuotaZaiCodingPlan_HistoryReadOmitsTargetScope(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/history/read", 200, `{"code":0,"data":{}}`)
	env.load(t)
	cap := startCapture(t, env)

	if got := zaiCodingPlanModule(t, env, `String(JSON.stringify(cp.markHistoryRead(`+zaiCtx+`)))`); got != `{"markedRead":true}` {
		t.Fatalf("markHistoryRead returned %s", got)
	}
	posts := cap.postsTo("coding-plan/reset/history/read")
	if len(posts) != 1 {
		t.Fatalf("expected 1 history/read POST, got %d", len(posts))
	}
	assertCodingPlanHeaders(t, posts[0], "zai-oauth-tok", false)
}

// TestQuotaZaiCodingPlan_HistoryReadCommand pins the command path:
// `/quota:resets:read` schedules the call and the handler clears the marker.
func TestQuotaZaiCodingPlan_HistoryReadCommand(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/history/read", 200, `{"code":0,"data":{}}`)
	env.load(t)

	if out := env.callCommand("quota", "resets", "read"); !strings.Contains(out, "Clearing") {
		t.Fatalf("/quota:resets:read = %q", out)
	}
	env.evalJS(t, `codingPlanHistoryReadOnce(zaiResetSurface())`)
	if out := env.lastOutput(); !strings.Contains(out, "reset-history marker cleared") {
		t.Fatalf("history-read handler output = %q", out)
	}
}

// --- command routing ----------------------------------------------------------

// TestQuotaZaiCodingPlan_ResetCommandRoutesToZai proves /quota:reset means
// "reset my ACTIVE provider's quota": with z.ai active it drives the Coding
// Plan flow (confirm → consume) instead of offering Codex credits. The
// headless harness confirm cancels, so nothing is spent.
func TestQuotaZaiCodingPlan_ResetCommandRoutesToZai(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/use", 200, `{"code":0,"data":{"used":true}}`)
	env.load(t)
	cap := startCapture(t, env)

	if out := env.callCommand("quota", "reset"); !strings.Contains(out, "Cancelled") {
		t.Fatalf("/quota:reset on z.ai should confirm before spending, got %q", out)
	}
	if n := len(cap.postsTo("coding-plan/reset/use")); n != 0 {
		t.Fatalf("a cancelled confirm must spend nothing, got %d use POSTs", n)
	}
}

// TestQuotaZaiCodingPlan_ResetCommandWithoutResets points the user at the claim
// path instead of failing silently.
func TestQuotaZaiCodingPlan_ResetCommandWithoutResets(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/status", 200,
		`{"code":0,"data":{"available_five_hour_resets":[],"available_week_resets":[],"has_unread_history":false}}`)
	env.load(t)

	if out := env.callCommand("quota", "reset"); !strings.Contains(out, "/quota:reset:claim") {
		t.Fatalf("/quota:reset with no resets = %q", out)
	}
}

// TestQuotaZaiCodingPlan_ClaimCommandReportsThrottle drives the claim handler
// directly: a 3301 denial is reported with the server's retry boundary rather
// than as a failure, and the single-flight flag is released.
func TestQuotaZaiCodingPlan_ClaimCommandReportsThrottle(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireZaiCodingPlan(t, env)
	env.respond("coding-plan/reset/opportunity", 200,
		`{"code":3301,"data":{"granted":false,"next_try_at":`+itoa(nowMs()+20*60*1000)+`}}`)
	env.load(t)

	if out := env.callCommand("quota", "reset", "claim"); !strings.Contains(out, "Asking z.ai") {
		t.Fatalf("/quota:reset:claim = %q", out)
	}
	env.evalJS(t, `codingPlanClaimOnce(zaiResetSurface())`)
	if out := env.lastOutput(); !strings.Contains(out, "throttled") {
		t.Fatalf("claim handler output = %q", out)
	}
	if !env.evalJSBool(t, "_resetInFlight === false") {
		t.Fatal("a terminal claim outcome must clear the single-flight flag")
	}
}

// TestQuotaZaiCodingPlan_MissingCredentialsExplainThemselves proves the command
// names the missing credential instead of reporting a generic auth error.
func TestQuotaZaiCodingPlan_MissingCredentialsExplainThemselves(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.setActiveProvider("z.ai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.load(t)

	if out := env.callCommand("quota", "reset", "claim"); !strings.Contains(out, "zcode_jwt_required") {
		t.Fatalf("/quota:reset:claim must name the missing zcode JWT, got %q", out)
	}
}

// TestQuotaZaiCodingPlan_FamilyTokenSources pins WHERE the family OAuth token
// is read from, since goa's OAuth bridge only serves openai/codex: the provider
// config wins, then the plugin storage key ZCode uses
// (`oauth:<family>:access_token`), then a Goa-managed token when the host
// serves that family. wantToken == "" means the case must NOT authenticate.
func TestQuotaZaiCodingPlan_FamilyTokenSources(t *testing.T) {
	for _, tc := range []familyTokenCase{
		{
			name:      "config accessToken",
			config:    map[string]any{"provider": "zai", "zcodeJwt": "zjwt-1", "accessToken": "cfg-tok"},
			wantToken: "cfg-tok",
		},
		{
			name:      "plugin storage key",
			config:    map[string]any{"provider": "zai", "zcodeJwt": "zjwt-1"},
			storage:   map[string]string{"oauth:zai:access_token": "stored-tok"},
			wantToken: "stored-tok",
		},
		{
			name:      "goa managed oauth",
			config:    map[string]any{"provider": "zai", "zcodeJwt": "zjwt-1"},
			oauth:     map[string]any{"accessToken": "goa-tok"},
			wantToken: "goa-tok",
		},
		{
			// The OTHER family's storage key must not stand in for this one.
			name:      "other family storage key is ignored",
			config:    map[string]any{"provider": "zai", "zcodeJwt": "zjwt-1"},
			storage:   map[string]string{"oauth:bigmodel:access_token": "wrong-family-tok"},
			wantToken: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { assertFamilyTokenSource(t, tc.config, tc.storage, tc.oauth, tc.wantToken) })
	}
}

// familyTokenCase is one credential-source case: the provider config, the
// plugin storage entries, an optional Goa-managed token, and the family token
// expected on the wire ("" = the case must NOT authenticate).
type familyTokenCase struct {
	name      string
	config    map[string]any
	storage   map[string]string
	oauth     map[string]any
	wantToken string
}

// assertFamilyTokenSource wires one credential source and checks the resulting
// family token on the wire — or the auth_required refusal when the case
// supplies no usable token for this family.
func assertFamilyTokenSource(t *testing.T, config map[string]any, storage map[string]string, oauth map[string]any, wantToken string) {
	t.Helper()
	env := newQuotaTestEnv(t)
	cfg := map[string]any{"apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"}
	for k, v := range config {
		cfg[k] = v
	}
	env.setProvider("z.ai", cfg)
	env.setActiveProvider("z.ai")
	for k, v := range storage {
		env.storage.Set(k, v)
	}
	if oauth != nil {
		env.setOAuthToken("zai", oauth)
	}
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.respond("coding-plan/reset/status", 200, zaiStatusBody)
	env.load(t)
	cap := startCapture(t, env)

	got := zaiCodingPlanObj(t, env, `cp.status(`+zaiCtx+`)`)
	if wantToken == "" {
		if got["error"] != "auth_required" {
			t.Fatalf("another family's token must not authenticate, got %v", got)
		}
		if n := cap.getsTo("coding-plan/reset"); n != 0 {
			t.Fatalf("no request may be sent, got %d", n)
		}
		return
	}
	if got["availableCount"] != float64(3) {
		t.Fatalf("status should map with this credential source, got %v", got)
	}
	for _, r := range cap.all() {
		if strings.Contains(r.URL, "coding-plan/reset") && r.Headers["X-Bigmodel-Authorization"] != wantToken {
			t.Fatalf("family token = %q, want %q", r.Headers["X-Bigmodel-Authorization"], wantToken)
		}
	}
}

// --- helpers ------------------------------------------------------------------

// assertCodingPlanBody pins the JSON body of one coding-plan request.
func assertCodingPlanBody(t *testing.T, p HTTPRequest, want map[string]any) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(p.Body), &body); err != nil {
		t.Fatalf("body not JSON (%q): %v", p.Body, err)
	}
	for k, v := range want {
		if body[k] != v {
			t.Fatalf("body[%q] = %v, want %v (body %q)", k, body[k], v, p.Body)
		}
	}
}

// assertCodingPlanHeaders pins the dual-auth header contract: the zcode JWT as
// a Bearer token, the family OAuth token RAW (no Bearer prefix), and the
// personal plan scope present only when includeTargetScope is true.
func assertCodingPlanHeaders(t *testing.T, p HTTPRequest, wantFamilyToken string, scoped bool) {
	t.Helper()
	if p.Headers["Authorization"] != "Bearer zjwt-1" {
		t.Fatalf("Authorization = %q, want Bearer zjwt-1", p.Headers["Authorization"])
	}
	if p.Headers["X-Bigmodel-Authorization"] != wantFamilyToken {
		t.Fatalf("X-Bigmodel-Authorization = %q, want the raw token %q", p.Headers["X-Bigmodel-Authorization"], wantFamilyToken)
	}
	got, has := p.Headers["Bigmodel-Target-Type"]
	if scoped {
		if !has || got != "PERSONAL" {
			t.Fatalf("Bigmodel-Target-Type = %q (present %v), want PERSONAL", got, has)
		}
		return
	}
	if has {
		t.Fatalf("Bigmodel-Target-Type = %q, want it omitted on history/read", got)
	}
}

// codingPlanKeys extracts idempotency_key values from captured coding-plan
// POSTs, failing the test when a body omits one.
func codingPlanKeys(t *testing.T, posts []HTTPRequest) []string {
	t.Helper()
	out := make([]string, 0, len(posts))
	for _, p := range posts {
		var body map[string]any
		if err := json.Unmarshal([]byte(p.Body), &body); err != nil {
			t.Fatalf("POST body not JSON: %q (%v)", p.Body, err)
		}
		k, _ := body["idempotency_key"].(string)
		if k == "" {
			t.Fatalf("POST body missing idempotency_key: %q", p.Body)
		}
		out = append(out, k)
	}
	return out
}

// TestQuotaZaiCodingPlan_GoaLoginUnblocksResets is the end-to-end payoff of the
// z.ai OAuth flow: a single `/login:zai:oauth` must be enough for /quota to
// show the account's real reset credits, with NO extra config.
//
// Before it, the only way to make this work was hand-pasting both credentials
// into providers[].extra — so a user holding resets saw "unavailable" with no
// path forward. Here the ONLY credential source is goa.auth.oauthToken, exactly
// as the real bridge serves it after a login.
func TestQuotaZaiCodingPlan_GoaLoginUnblocksResets(t *testing.T) {
	env := newQuotaTestEnv(t)
	// A plain z.ai provider: API key for consumption, no reset credentials in
	// config — the shape every real user has.
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	// The managed credential, as pluginOAuthToken returns it after a login.
	env.setOAuthToken("zai", map[string]any{
		"accessToken": "biz-token",
		"zcodeJwt":    "zcode-jwt",
		"accountId":   "u-1",
	})
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.respond("coding-plan/reset/status", 200, zaiStatusBody)

	env.load(t)

	resets := env.callCommand("quota", "resets")
	for _, want := range []string{"Coding Plan Resets", "| 5-hour | 2 |", "| Week | 1 |"} {
		if !strings.Contains(resets, want) {
			t.Fatalf("/quota:resets missing %q after a Goa login:\n%s", want, resets)
		}
	}
	// The z.ai reset surface must no longer report the credential gap. (Scoped
	// to that reason code: the unrelated Codex line above legitimately says
	// "unavailable" for its own absent login.)
	if strings.Contains(resets, "zcode_jwt_required") || strings.Contains(resets, "maas_jwt_required") {
		t.Fatalf("/quota:resets still reports the credential gap:\n%s", resets)
	}

	env.callCommand("quota", "refresh")
	full := env.callCommand("quota")
	if !strings.Contains(full, "3 available (2 × 5h · 1 × week)") {
		t.Fatalf("/quota must carry the reset count row after a Goa login:\n%s", full)
	}
}

// TestQuotaZaiCodingPlan_ManagedJwtPairedWithManagedToken pins that BOTH
// credentials come from the one managed login. Half-authenticating (managed
// token but a JWT from elsewhere) must not be treated as authenticated: the
// pair is minted together, and a mismatch is what the backend rejects.
func TestQuotaZaiCodingPlan_ManagedJwtPairedWithManagedToken(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
	})
	env.setActiveProvider("z.ai")
	// A managed token that carries NO zcode JWT: the other half is missing, so
	// the reset surface must still refuse rather than send a token-only request.
	env.setOAuthToken("zai", map[string]any{"accessToken": "biz-token"})
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.respond("coding-plan/reset/status", 200, zaiStatusBody)

	env.load(t)
	resets := env.callCommand("quota", "resets")
	if !strings.Contains(resets, "zcode_jwt_required") {
		t.Fatalf("a managed token without the JWT must still report the JWT gap:\n%s", resets)
	}
	if strings.Contains(resets, "| 5-hour | 2 |") {
		t.Fatalf("must not render resets it could not authenticate for:\n%s", resets)
	}
}

// TestQuotaZaiCodingPlan_ConfigOverridesManagedLogin pins precedence: an explicit
// config credential still wins over the managed one, so a user debugging a
// second account is not silently overridden by their stored login.
func TestQuotaZaiCodingPlan_ConfigOverridesManagedLogin(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("z.ai", map[string]any{
		"provider": "zai", "apiKey": "k",
		"endpoint": "https://api.z.ai/api/coding/paas/v4",
		"zcodeJwt": "config-jwt",
	})
	env.setActiveProvider("z.ai")
	env.setOAuthToken("zai", map[string]any{
		"accessToken": "managed-token", "zcodeJwt": "managed-jwt",
	})
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, zaiMonitorBody)
	env.respond("coding-plan/reset/status", 200, zaiStatusBody)

	// Force a synchronous fetch: the plugin primes its cache asynchronously at
	// load, so the reset request (and its headers) only become observable after
	// an explicit refresh.
	env.load(t)
	env.callCommand("quota", "resets")

	seen := env.seenAuthHeaders()
	if !strings.Contains(seen, "config-jwt") {
		t.Fatalf("config zcodeJwt must win over the managed one; headers=%q", seen)
	}
	if strings.Contains(seen, "managed-jwt") {
		t.Fatalf("managed JWT leaked past the config override: %q", seen)
	}
}
