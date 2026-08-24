// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dop251/goja"
)

// --- shared capture helpers --------------------------------------------------

// httpCapture records HTTP requests flowing through a setHTTPDo hook so
// tests can assert on the wire contract (method, URL, payload).
type httpCapture struct {
	mu   sync.Mutex
	reqs []HTTPRequest
}

func (c *httpCapture) record(req HTTPRequest) {
	c.mu.Lock()
	c.reqs = append(c.reqs, req)
	c.mu.Unlock()
}

func (c *httpCapture) all() []HTTPRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]HTTPRequest, len(c.reqs))
	copy(out, c.reqs)
	return out
}

// postsTo filters captured requests to POSTs whose URL contains substr.
func (c *httpCapture) postsTo(substr string) []HTTPRequest {
	var out []HTTPRequest
	for _, r := range c.all() {
		if r.Method == "POST" && strings.Contains(r.URL, substr) {
			out = append(out, r)
		}
	}
	return out
}

// getsTo counts captured GETs whose URL contains substr.
func (c *httpCapture) getsTo(substr string) int {
	n := 0
	for _, r := range c.all() {
		if r.Method == "GET" && strings.Contains(r.URL, substr) {
			n++
		}
	}
	return n
}

// startCapture installs a recording wrapper around the env's canned
// responders for the remainder of the test (stacked on top of the hook
// env.load installed; cleanups unwind in LIFO order).
func startCapture(t *testing.T, e *quotaTestEnv) *httpCapture {
	t.Helper()
	cap := &httpCapture{}
	inner := e.mockDo()
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		cap.record(req)
		return inner(b, req)
	})
	t.Cleanup(restore)
	return cap
}

// codexTokenStub is the Goa-managed token the reset tests authenticate with.
func codexTokenStub() map[string]any {
	return map[string]any{"accessToken": "tok-123", "accountId": "acct-9"}
}

// requireCodex evaluates an expression with `fetcher` bound to the real
// fetchers/codex.js module inside the loaded plugin VM (require is a global
// in the plugin runtime).
func requireCodex(t *testing.T, e *quotaTestEnv, expr string) string {
	t.Helper()
	return e.evalJSString(t, `(function(){
		var fetcher = require("fetchers/codex.js");
		return `+expr+`;
	})()`)
}

// evalJSString evaluates a JS expression under the VM lock and returns its
// string value ("" for undefined/null).
func (e *quotaTestEnv) evalJSString(t *testing.T, expr string) string {
	t.Helper()
	unlock := lockVM()
	defer unlock()
	v, err := e.bridge.vm.RunString(expr)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// evalJSONObj evaluates a JS expression returning an object and exports it as
// a Go map (top-level JSON fields).
func (e *quotaTestEnv) evalJSONObj(t *testing.T, expr string) map[string]any {
	t.Helper()
	raw := e.evalJSString(t, "JSON.stringify("+expr+")")
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("evalJSONObj %q: %v (raw %q)", expr, err, raw)
	}
	return out
}

// resetUsageBody is a canned /wham/usage response carrying plan, credits and
// a rate_limit_reset_credits count — everything downstream commands read.
const resetUsageBody = `{
	"plan_type": "Pro",
	"credits": {"balance": 750},
	"rate_limit_reset_credits": {"available_count": 2},
	"rate_limit": {}
}`

// wireCodexAuth installs the oauth token and canned usage response so any
// forced codex refresh lands data instead of auth_required.
func wireCodexAuth(t *testing.T, e *quotaTestEnv) {
	t.Helper()
	e.setOAuthToken("openai", codexTokenStub())
	e.respond("wham/usage", 200, resetUsageBody)
}

// redeemIDs extracts the redeem_request_id field from captured POST bodies,
// failing the test when a body is missing it.
func redeemIDs(t *testing.T, posts []HTTPRequest) []string {
	t.Helper()
	ids := make([]string, 0, len(posts))
	for _, p := range posts {
		var body map[string]any
		if err := json.Unmarshal([]byte(p.Body), &body); err != nil {
			t.Fatalf("POST body not JSON: %q (%v)", p.Body, err)
		}
		id, _ := body["redeem_request_id"].(string)
		if id == "" {
			t.Fatalf("POST body missing redeem_request_id: %q", p.Body)
		}
		ids = append(ids, id)
	}
	return ids
}

// uuidV4RE matches the RFC 4122 v4 UUID shape the fetcher mints.
var uuidV4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// --- 5.6a URL/payload contract ------------------------------------------------

// assertConsumePOST pins the wire shape of one consume request: exact URL,
// JSON body carrying redeem_request_id (+ optional credit_id) and the shared
// Codex headers. Returns the redeem id for idempotency assertions.
func assertConsumePOST(t *testing.T, p HTTPRequest, wantCredit string) string {
	t.Helper()
	wantURL := "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	if p.URL != wantURL {
		t.Fatalf("consume URL = %q, want %q", p.URL, wantURL)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(p.Body), &body); err != nil {
		t.Fatalf("consume body not JSON: %v", err)
	}
	rid, _ := body["redeem_request_id"].(string)
	if !uuidV4RE.MatchString(rid) {
		t.Fatalf("redeem_request_id %q is not a UUIDv4", rid)
	}
	if wantCredit == "" {
		if _, ok := body["credit_id"]; ok {
			t.Fatalf("credit_id must be omitted when not requested, got %v", body)
		}
	} else if body["credit_id"] != wantCredit {
		t.Fatalf("credit_id = %v, want %q", body["credit_id"], wantCredit)
	}
	if p.Headers["Authorization"] != "Bearer tok-123" {
		t.Fatalf("Authorization = %q", p.Headers["Authorization"])
	}
	if p.Headers["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q", p.Headers["Content-Type"])
	}
	if p.Headers["ChatGPT-Account-Id"] != "acct-9" {
		t.Fatalf("ChatGPT-Account-Id = %q", p.Headers["ChatGPT-Account-Id"])
	}
	return rid
}

// TestQuotaReset_ConsumeURLAndPayloadContract pins the wire contract of the
// consume endpoint (mirrors Codex's rate_limit_resets_tests.rs): exact URL,
// POST method, {redeem_request_id, credit_id} body, auth headers.
func TestQuotaReset_ConsumeURLAndPayloadContract(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits/consume", 200, `{"code":"reset"}`)
	env.load(t)
	cap := startCapture(t, env)

	got := requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset(null)))`)
	if got != `{"outcome":"reset"}` {
		t.Fatalf("consumeReset returned %s", got)
	}

	posts := cap.postsTo("rate-limit-reset-credits")
	if len(posts) != 1 {
		t.Fatalf("expected exactly 1 consume POST, got %d (%v)", len(posts), cap.all())
	}
	assertConsumePOST(t, posts[0], "")

	// With a credit id the payload carries it verbatim.
	got = requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset(null, "credit-x")))`)
	if got != `{"outcome":"reset"}` {
		t.Fatalf("consumeReset(credit) returned %s", got)
	}
	posts = cap.postsTo("rate-limit-reset-credits")
	if len(posts) != 2 {
		t.Fatalf("expected 2nd consume POST, got %d", len(posts))
	}
	assertConsumePOST(t, posts[1], "credit-x")
}

// TestQuotaReset_ResetCreditsURLContract pins the details endpoint: GET on
// /wham/rate-limit-reset-credits with the shared Codex headers, mapped through
// the tolerant detail mapper.
func TestQuotaReset_ResetCreditsURLContract(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits/consume", 200, `{"code":"reset"}`)
	env.respond("backend-api/wham/rate-limit-reset-credits", 200,
		`{"available_count":1,"credits":[{"id":"c1","title":"Weekly","status":"available","reset_type":"weekly","expires_at":"2099-01-01T00:00:00Z"}]}`)
	env.load(t)
	cap := startCapture(t, env)

	got := requireCodex(t, env, `String(JSON.stringify(fetcher.resetCredits()))`)
	if !strings.Contains(got, `"availableCount":1`) || !strings.Contains(got, `"id":"c1"`) {
		t.Fatalf("resetCredits mapped %s", got)
	}
	gets := 0
	var url string
	var auth string
	for _, r := range cap.all() {
		if r.Method == "GET" && strings.HasSuffix(r.URL, "/wham/rate-limit-reset-credits") {
			gets++
			url = r.URL
			auth = r.Headers["Authorization"]
		}
	}
	if gets != 1 {
		t.Fatalf("expected exactly 1 details GET, got %d", gets)
	}
	if url != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
		t.Fatalf("details URL = %q", url)
	}
	if auth != "Bearer tok-123" {
		t.Fatalf("details Authorization = %q", auth)
	}
}

// --- 5.6b idempotency key retention -------------------------------------------

// TestQuotaReset_IdempotencyKeyRetainedAcrossRetry proves the Codex-parity
// redemption contract: a simulated transport error retains the pending
// redeem_request_id, the retry sends the IDENTICAL key on the wire (the
// server dedupes double-redeems), and only a terminal outcome clears it so
// the next reset mints a fresh id.
func TestQuotaReset_IdempotencyKeyRetainedAcrossRetry(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)

	cap := &httpCapture{}
	var attempts int32
	inner := env.mockDo()
	restore := setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		cap.record(req)
		if strings.Contains(req.URL, "/consume") {
			if atomic.AddInt32(&attempts, 1) == 1 {
				// Simulated transport failure (connection dropped).
				return HTTPResponse{Error: "dial refused"}
			}
			return HTTPResponse{Status: 200, Body: `{"code":"reset"}`}
		}
		return inner(b, req)
	})
	t.Cleanup(restore)

	first := requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset(null)))`)
	if first != `{"error":"dial refused"}` {
		t.Fatalf("first attempt should surface the transport error, got %s", first)
	}
	retry := requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset(null)))`)
	if retry != `{"outcome":"reset"}` {
		t.Fatalf("retry should succeed, got %s", retry)
	}
	after := requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset(null)))`)
	if after != `{"outcome":"reset"}` {
		t.Fatalf("post-terminal attempt should succeed, got %s", after)
	}

	ids := redeemIDs(t, cap.postsTo("/consume"))
	if len(ids) != 3 {
		t.Fatalf("expected 3 consume POSTs, got %d", len(ids))
	}
	if ids[0] != ids[1] {
		t.Fatalf("retry MUST reuse the retained redeem_request_id: %q vs %q", ids[0], ids[1])
	}
	if ids[2] == ids[0] {
		t.Fatalf("terminal outcome must clear the key so the next request mints a new id: %q", ids[2])
	}
}

// TestQuotaReset_ExplicitRequestIDPinsKey covers the explicit-id form used by
// tests/diagnostics: a passed redeemRequestId is sent verbatim.
func TestQuotaReset_ExplicitRequestIDPinsKey(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits/consume", 200, `{"code":"nothing_to_reset"}`)
	env.load(t)
	cap := startCapture(t, env)

	requireCodex(t, env, `String(JSON.stringify(fetcher.consumeReset("explicit-key")))`)
	posts := cap.postsTo("/consume")
	if len(posts) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(posts))
	}
	ids := redeemIDs(t, posts)
	if ids[0] != "explicit-key" {
		t.Fatalf("explicit redeem id = %q, want explicit-key", ids[0])
	}
}

// --- 5.6c outcome matrix -------------------------------------------------------

// assertCachedResetsCount pins the cached codex resetsCount; want < 0 skips
// the assertion (cases where the entry's count is irrelevant).
func assertCachedResetsCount(t *testing.T, e *quotaTestEnv, want int) {
	t.Helper()
	if want < 0 {
		return
	}
	entry := e.evalJSONObj(t, `{resetsCount: _cache.codex ? _cache.codex.resetsCount : -99}`)
	if entry["resetsCount"] != float64(want) {
		t.Fatalf("cached resetsCount = %v, want %d", entry["resetsCount"], want)
	}
}

// TestQuotaReset_OutcomeMatrix drives every backend consume code through the
// real plugin handler (consumeResetOnce) and pins message + cache side
// effects. Headless confirms resolve {cancelled:true,error:"no-ui"}, so the
// unknown-code case exercises the retry-offer decline path.
func TestQuotaReset_OutcomeMatrix(t *testing.T) {
	cases := []struct {
		name            string
		code            string // backend code served by the mocked consume endpoint
		wantOutput      string // substring required in the emitted goa.output
		wantUsageGETs   int    // exact number of /wham/usage GETs after the run
		wantResetsCount int    // expected _cache.codex.resetsCount (-1: skip)
	}{
		{
			name:            "reset",
			code:            "reset",
			wantOutput:      "✔ Usage limit reset.",
			wantUsageGETs:   1, // post-terminal re-fetch (prime precedes capture)
			wantResetsCount: 2,
		},
		{
			name:            "already_redeemed",
			code:            "already_redeemed",
			wantOutput:      "✔ Usage limit reset.",
			wantUsageGETs:   1,
			wantResetsCount: 2,
		},
		{
			name:            "no_credit",
			code:            "no_credit",
			wantOutput:      "No rate-limit reset credits left",
			wantUsageGETs:   0, // pins the existing cached entry; no fetch
			wantResetsCount: 0,
		},
		{
			name:            "nothing_to_reset",
			code:            "nothing_to_reset",
			wantOutput:      "Nothing to reset",
			wantUsageGETs:   0, // cache untouched
			wantResetsCount: 2,
		},
		{
			name:            "unknown_code_degrades_to_retry_offer",
			code:            "wat",
			wantOutput:      "Reset not retried", // headless confirm declines the retry
			wantUsageGETs:   0,                   // error path never re-fetches
			wantResetsCount: -1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaTestEnv(t)
			wireCodexAuth(t, env)
			env.respond("rate-limit-reset-credits/consume", 200, `{"code":"`+tc.code+`"}`)
			env.load(t)
			cap := startCapture(t, env)

			// Drive the real post-confirm handler (what the timer would run).
			env.evalJS(t, `consumeResetOnce(null)`)

			out := env.lastOutput()
			if !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("output for code %q = %q, want substring %q", tc.code, out, tc.wantOutput)
			}
			assertCachedResetsCount(t, env, tc.wantResetsCount)
			if !env.evalJSBool(t, "_resetInFlight === false") {
				t.Fatalf("terminal outcome must clear the single-flight flag (code %q)", tc.code)
			}
			if got := cap.getsTo("wham/usage"); got != tc.wantUsageGETs {
				t.Fatalf("code %q: usage GETs = %d, want exactly %d", tc.code, got, tc.wantUsageGETs)
			}
		})
	}
}

// --- 5.6d details degradation ---------------------------------------------------

// TestQuotaResets_DetailsTableTolerantMapping renders /quota:resets from a
// deliberately messy details payload: mixed-case statuses, unknown enums,
// RFC3339 + epoch expiries, missing titles. Available credits sort first by
// earliest expiry; unknown strings degrade instead of crashing.
func TestQuotaResets_DetailsTableTolerantMapping(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("backend-api/wham/rate-limit-reset-credits", 200, `{
		"available_count": 1,
		"credits": [
			{"id":"aaaaaaaa-1111","title":"Weekly reset","status":"REDEEMED","reset_type":"weekly","expires_at":"2099-01-02T00:00:00Z"},
			{"id":"bbbbbbbb-2222","title":"Monthly reset","status":"Available","reset_type":"monthly","expires_at":1893456000},
			{"id":"cccccccc-3333","status":"","reset_type":"","expires_at":"not-a-date"}
		]
	}`)
	env.load(t)

	out := env.callCommand("quota", "resets")

	for _, want := range []string{
		"Codex Rate-Limit Resets",
		"1 available.",
		"| bbbbbbbb… | Monthly reset |",
		"| aaaaaaaa… | Weekly reset |",
		"| cccccccc… | — |",
		"available (monthly)",
		"redeemed (weekly)",
		"unknown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/quota:resets output missing %q:\n%s", want, out)
		}
	}
	// Sorting: available-first, then earliest expiry, missing expiry last.
	if strings.Index(out, "bbbbbbbb") > strings.Index(out, "aaaaaaaa") {
		t.Fatalf("available credit must sort before redeemed ones:\n%s", out)
	}
	if strings.Index(out, "cccccccc") < strings.Index(out, "aaaaaaaa") {
		t.Fatalf("missing-expiry credit must sort last:\n%s", out)
	}
}

// TestQuotaResets_DetailsErrorDegradesToCountOnly pins the count-vs-details
// degradation: when the details endpoint fails, the command still tells the
// user how many resets they have, using the count from the usage snapshot.
func TestQuotaResets_DetailsErrorDegradesToCountOnly(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("backend-api/wham/rate-limit-reset-credits", 500, `{"error":"upstream boom"}`)
	env.load(t)

	out := env.callCommand("quota", "resets")
	if !strings.Contains(out, "(count only)") {
		t.Fatalf("degraded output must be count-only, got:\n%s", out)
	}
	if !strings.Contains(out, "**2**") {
		t.Fatalf("degraded output must carry the cached count 2, got:\n%s", out)
	}

	// Without any cached count even the degradation is honest about it. The
	// command force-refreshes usage before reading the cache, so the refresh
	// itself must fail to reach the no-count branch: drop the cached entry
	// AND break the usage endpoint.
	env.evalJS(t, `delete _cache.codex`)
	env.respond("wham/usage", 500, `{}`)
	out = env.callCommand("quota", "resets")
	if !strings.Contains(out, "no cached count") {
		t.Fatalf("missing-count output must say so, got:\n%s", out)
	}
}

// --- confirm gating -------------------------------------------------------------

// TestQuotaReset_DeclinedConfirmSendsNothing pins the fail-closed behavior:
// in a headless harness goa.ui.confirm resolves {cancelled:true,error:"no-ui"},
// which must abort the reset BEFORE any request is sent.
func TestQuotaReset_DeclinedConfirmSendsNothing(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits/consume", 200, `{"code":"reset"}`)
	env.load(t)
	cap := startCapture(t, env)

	out := env.callCommand("quota", "reset")
	if out != "Cancelled — no reset consumed." {
		t.Fatalf("declined reset output = %q", out)
	}
	if posts := cap.postsTo("/consume"); len(posts) != 0 {
		t.Fatalf("declined reset must not POST, got %v", posts)
	}

	// A credit-targeted variant declines identically.
	out = env.callCommand("quota", "reset", "credit-x")
	if out != "Cancelled — no reset consumed." {
		t.Fatalf("declined targeted reset output = %q", out)
	}
	if posts := cap.postsTo("/consume"); len(posts) != 0 {
		t.Fatalf("declined targeted reset must not POST, got %v", posts)
	}
}

// TestQuotaReset_AuthRequiredShortCircuits pins the auth gate: without a Goa
// OAuth token the reset command refuses before touching confirm or HTTP.
func TestQuotaReset_AuthRequiredShortCircuits(t *testing.T) {
	env := newQuotaTestEnv(t) // no oauth token installed
	env.load(t)
	cap := startCapture(t, env)

	out := env.callCommand("quota", "reset")
	if !strings.Contains(out, "auth required") {
		t.Fatalf("auth-required output = %q", out)
	}
	if len(cap.all()) != 0 {
		t.Fatalf("auth gate must prevent all HTTP, got %v", cap.all())
	}
}

// TestQuotaReset_SingleFlight pins the _resetInFlight simplification (plan
// §5.6: no request-id scheme — a single-flight boolean replaces Codex's u64
// ids): while a reset is pending a second command refuses without touching
// HTTP, and terminal outcomes clear the flag.
func TestQuotaReset_SingleFlight(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits/consume", 200, `{"code":"reset"}`)
	env.load(t)
	cap := startCapture(t, env)

	env.evalJS(t, "_resetInFlight = true")
	out := env.callCommand("quota", "reset")
	if !strings.Contains(out, "already in progress") {
		t.Fatalf("busy output = %q", out)
	}
	if len(cap.postsTo("/consume")) != 0 {
		t.Fatal("busy reset must not POST")
	}

	// A terminal outcome clears the flag so the next reset can proceed.
	env.evalJS(t, "consumeResetOnce(null)")
	if !env.evalJSBool(t, "_resetInFlight === false") {
		t.Fatal("terminal outcome must clear _resetInFlight")
	}
}

// --- postJSON unit coverage -------------------------------------------------------

// postJSONProbe is the JSON result of one in-VM postJSON probe: `out` is what
// postJSON returned, `seen` captures whether onBody ran.
type postJSONProbe struct {
	Out  map[string]any `json:"out"`
	Seen map[string]any `json:"seen"`
}

// runPostJSONProbe evaluates a postJSON call against https://x.test/reset in a
// fresh bridge whose HTTP layer answers per the case (or fails with respErr).
func runPostJSONProbe(t *testing.T, status int, body, respErr string) postJSONProbe {
	t.Helper()
	env := newQuotaTestEnv(t)
	env.respond("x.test", status, body)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		if respErr != "" {
			return HTTPResponse{Error: respErr}
		}
		return env.mockDo()(b, req)
	})()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		(function() {
			var hq = globalThis.__require("lib/http-quota.js");
			var seen = null;
			var out = hq.postJSON("https://x.test/reset", {"Content-Type": "application/json"},
				{hello: "world"}, function(b) { seen = b; return {mapped: true}; });
			return JSON.stringify({out: out, seen: seen});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	var got postJSONProbe
	if err := json.Unmarshal([]byte(v.String()), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// assertPostJSONError pins the error form: mapped error string, onBody skipped.
func assertPostJSONError(t *testing.T, got postJSONProbe, wantErr string) {
	t.Helper()
	if got.Out["error"] != wantErr {
		t.Fatalf("postJSON error = %v, want %v", got.Out["error"], wantErr)
	}
	if got.Seen != nil {
		t.Fatalf("onBody must not run on error, saw %v", got.Seen)
	}
}

// TestPostJSON_ErrorVocabulary pins lib/http-quota.js postJSON beside getJSON:
// identical error mapping (auth_required / http_<status> / bad_response /
// transport passthrough) and no onBody callback on any failure.
func TestPostJSON_ErrorVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		respErr string
		wantErr string
	}{
		{name: "401 maps auth_required", status: 401, wantErr: "auth_required"},
		{name: "403 maps auth_required", status: 403, wantErr: "auth_required"},
		{name: "500 maps http_500", status: 500, wantErr: "http_500"},
		{name: "bad json maps bad_response", status: 200, body: "{oops", wantErr: "bad_response"},
		{name: "transport error passes through", respErr: "boom", wantErr: "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runPostJSONProbe(t, tc.status, tc.body, tc.respErr)
			assertPostJSONError(t, got, tc.wantErr)
		})
	}
}

// TestPostJSON_SuccessPathCompletes is the happy-path counterpart to the
// error matrix: onBody receives the parsed response body and its return
// value becomes postJSON's result.
func TestPostJSON_SuccessPathCompletes(t *testing.T) {
	got := runPostJSONProbe(t, 200, `{"ok":true}`, "")
	if got.Out["mapped"] != true {
		t.Fatalf("onBody return must win: %v", got.Out)
	}
	if got.Seen["ok"] != true {
		t.Fatalf("onBody must receive the parsed response body, saw %v", got.Seen)
	}
}

// TestPostJSON_BodyOnTheWire verifies the JSON-encoded body actually reaches
// the transport layer (captured, not assumed).
func TestPostJSON_BodyOnTheWire(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("x.test", 200, `{"ok":true}`)
	cap := &httpCapture{}
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(func(b *HTTPBridge, req HTTPRequest) HTTPResponse {
		cap.record(req)
		return env.mockDo()(b, req)
	})()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	if _, err := bridge.vm.RunString(`
		globalThis.__require("lib/http-quota.js").postJSON(
			"https://x.test/consume", {}, {a: 1}, function(body) { return body; });
	`); err != nil {
		t.Fatal(err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(cap.reqs))
	}
	req := cap.reqs[0]
	if req.Method != "POST" {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("wire body not JSON: %q", req.Body)
	}
	if body["a"] != float64(1) {
		t.Fatalf("wire body = %v", body)
	}
}

// TestGetJSON_LegacyErrorShapePreserved guards the http-quota refactor: getJSON
// errors keep the historical {error, plan:null, limits:[]} shape consumed by
// plugin.js and the segment renderer.
func TestGetJSON_LegacyErrorShapePreserved(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.respond("x.test", 503, `nope`)
	bridge := NewJSBridge(PluginDef{ID: "q"}, env.context())
	bridge.installRequire(quotaPluginDir)
	defer setHTTPDo(env.mockDo())()
	unlock := lockVM()
	defer unlock()
	bridge.vm.Set("__require", bridge.vm.Get("require"))
	v, err := bridge.vm.RunString(`
		JSON.stringify(globalThis.__require("lib/http-quota.js").getJSON("https://x.test/usage", {}, function(b){return b;}));
	`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(v.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got["error"] != "http_503" {
		t.Fatalf("getJSON error = %v", got["error"])
	}
	if _, ok := got["plan"]; !ok {
		t.Fatalf("legacy shape must keep plan key: %v", got)
	}
	if _, ok := got["limits"]; !ok {
		t.Fatalf("legacy shape must keep limits key: %v", got)
	}
}

// --- startup notice + /quota discoverability ---------------------------------

// noResetsUsageBody is a canned /wham/usage response WITHOUT
// rate_limit_reset_credits — the no-credits control for the startup notice.
const noResetsUsageBody = `{
	"plan_type": "Pro",
	"credits": {"balance": 750},
	"rate_limit": {}
}`

// TestQuotaReset_StartupNoticeFiresOnceWithCredits pins the session-start
// notification: after the load-time primed refresh lands a positive reset
// count, exactly one chat output must inform the user the credits exist AND
// how to spend them (both commands). Re-invoking the check must stay silent —
// once per session, never spam.
func TestQuotaReset_StartupNoticeFiresOnceWithCredits(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t) // drainPrime waits for the prime callback incl. the notice

	want := "You have 2 Codex rate-limit resets available"
	if got := env.outputCount(want); got != 1 {
		t.Fatalf("expected exactly 1 startup notice mentioning %q, got %d", want, got)
	}
	out := env.lastOutput()
	for _, cmd := range []string{"/quota:resets", "/quota:reset"} {
		if !strings.Contains(out, cmd) {
			t.Fatalf("startup notice must tell the user how to act (missing %q):\n%s", cmd, out)
		}
	}

	// Idempotent: a second invocation (e.g. a later scheduler tick path)
	// must not repeat the notice.
	env.evalJS(t, `maybeStartupResetNotice()`)
	if got := env.outputCount(want); got != 1 {
		t.Fatalf("startup notice repeated: expected still 1, got %d", got)
	}
}

// TestQuotaReset_StartupNoticeSilentWithoutCredits pins the control: no reset
// credits on the usage snapshot ⇒ no startup notice at all.
func TestQuotaReset_StartupNoticeSilentWithoutCredits(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setOAuthToken("openai", codexTokenStub())
	env.respond("wham/usage", 200, noResetsUsageBody)
	env.load(t)

	if got := env.outputCount("Codex rate-limit reset"); got != 0 {
		t.Fatalf("no-credits account must not produce a startup notice, got %d", got)
	}
	// The flag still flips: the drain relies on it regardless of credits.
	if !env.evalJSBool(t, `_startupPrimeDone === true`) {
		t.Fatal("prime-done flag must set even without credits")
	}
}

// TestQuotaReset_QuotaShowsResetHowTo pins bare-/quota discoverability: with
// credits cached, the rendered breakdown must include the how-to note naming
// both commands; with none cached, it must not appear.
func TestQuotaReset_QuotaShowsResetHowTo(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)

	out := env.callCommand("quota", "")
	note := "Codex rate-limit resets:"
	if !strings.Contains(out, note) || !strings.Contains(out, "/quota:reset") {
		t.Fatalf("/quota must include the reset how-to note, got:\n%s", out)
	}

	// Control: drop the codex entry entirely — no credits ⇒ no note.
	env.evalJS(t, `delete _cache.codex`)
	out = env.callCommand("quota", "")
	if strings.Contains(out, note) {
		t.Fatalf("/quota must not show the how-to note without credits, got:\n%s", out)
	}
}

// TestQuotaReset_QuotaIncludesDetailsTable pins issue 1 of 2026-08-24: the
// reset-credit DETAILS belong in the global /quota breakdown, not only behind
// /quota:resets. When the details endpoint answers during refresh, /quota
// renders the credits table inline (ids, titles, expiry, status).
func TestQuotaReset_QuotaIncludesDetailsTable(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.respond("rate-limit-reset-credits", 200,
		`{"available_count":1,"credits":[{"id":"credit-ab-1234","title":"Weekly reset","status":"available","reset_type":"weekly","expires_at":"2099-01-01T00:00:00Z"}]}`)
	env.load(t)

	out := env.callCommand("quota", "")
	for _, want := range []string{
		"Codex Rate-Limit Resets",
		"credit-a…",      // shortId rendering
		"Weekly reset",   // title
		"available",      // status
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("/quota missing details table entry %q:\n%s", want, out)
		}
	}

	// Degradation: with the details endpoint now failing (404 replaces the
	// responder — same substr), /quota still renders via the count-only note.
	env.respond("rate-limit-reset-credits", 404, `{}`)
	env.evalJS(t, `delete _cache.codex`)
	env.warmCache(t)
	out = env.callCommand("quota", "")
	if !strings.Contains(out, "Codex rate-limit resets:") {
		t.Fatalf("/quota without details must fall back to the how-to note:\n%s", out)
	}
	if strings.Contains(out, "credit-a…") {
		t.Fatalf("/quota must not render a stale details table after a failed refresh:\n%s", out)
	}
}

// TestQuotaReset_Completion pins the /quota subcommand completer: static subs
// prefix-match, provider ids appear at top level and under :login:/ :logout:,
// and unknown nested scopes return nothing.
func TestQuotaReset_Completion(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)

	fn := func() func(prefix string) []Completion {
		env.mu.Lock()
		defer env.mu.Unlock()
		return env.completions["quota"]
	}()
	if fn == nil {
		t.Fatal("plugin did not register completions for quota")
	}

	values := func(comps []Completion) []string {
		out := []string{}
		for _, c := range comps {
			out = append(out, c.Value)
		}
		return out
	}
	has := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}

	all := values(fn(""))
	for _, sub := range []string{"refresh", "json", "auth-status", "resets", "reset"} {
		if !has(all, sub) {
			t.Errorf("bare completion missing %q: %v", sub, all)
		}
	}
	filtered := values(fn("re"))
	for _, want := range []string{"refresh", "resets", "reset"} {
		if !has(filtered, want) {
			t.Errorf("'re' completion missing %q: %v", want, filtered)
		}
	}
	if has(filtered, "json") {
		t.Errorf("'re' completion must not include 'json': %v", filtered)
	}
	logins := values(fn("login:"))
	if !has(logins, "codex") || !has(logins, "kimi") {
		t.Errorf("login: level must offer OAuth providers: %v", logins)
	}
	if got := values(fn("bogus:")); len(got) != 0 {
		t.Errorf("unknown nested scope must return nothing, got %v", got)
	}
	// Values carry no command prefix — the engine prepends "/quota:".
	for _, v := range all {
		if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "quota") {
			t.Errorf("completion value %q must be bare segment", v)
		}
	}
}
