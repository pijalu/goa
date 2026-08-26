// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"slices"
	"strings"
	"testing"
)

// --- shared capture helpers --------------------------------------------------

// httpCapture records HTTP requests flowing through a setHTTPDo hook so
// tests can assert on the wire contract (method, URL, payload).

// quotaCompleter fetches the "/quota" completion function registered by the
// plugin, grabbing the environment lock for the map read.
func quotaCompleter(t *testing.T, e *quotaTestEnv) func(prefix string) []Completion {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	fn := e.completions["quota"]
	if fn == nil {
		t.Fatal("plugin did not register completions for quota")
	}
	return fn
}

// completionValues flattens completions to their bare value strings.
func completionValues(comps []Completion) []string {
	out := make([]string, 0, len(comps))
	for _, c := range comps {
		out = append(out, c.Value)
	}
	return out
}

// hasString reports whether want appears in list.
func hasString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// indexOfString returns the index of want in list, or -1.
func indexOfString(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

// TestQuotaReset_Completion pins the /quota subcommand completer: static subs
// prefix-match, provider ids appear at top level and under :login:/ :logout:,
// and unknown nested scopes return nothing.
func TestQuotaReset_Completion(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)
	fn := quotaCompleter(t, env)

	all := completionValues(fn(""))
	for _, sub := range []string{"refresh", "json", "auth-status", "resets", "reset"} {
		if !hasString(all, sub) {
			t.Errorf("bare completion missing %q: %v", sub, all)
		}
	}
	filtered := completionValues(fn("re"))
	for _, want := range []string{"refresh", "resets", "reset"} {
		if !hasString(filtered, want) {
			t.Errorf("'re' completion missing %q: %v", want, filtered)
		}
	}
	if hasString(filtered, "json") {
		t.Errorf("'re' completion must not include 'json': %v", filtered)
	}
	logins := completionValues(fn("login:"))
	if !hasString(logins, "codex") || !hasString(logins, "kimi") {
		t.Errorf("login: level must offer OAuth providers: %v", logins)
	}
	if got := completionValues(fn("bogus:")); len(got) != 0 {
		t.Errorf("unknown nested scope must return nothing, got %v", got)
	}
	// Values carry no command prefix — the engine prepends "/quota:".
	for _, v := range all {
		if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "quota") {
			t.Errorf("completion value %q must be bare segment", v)
		}
	}
}

// --- id readability + resolution + completion ---------------------------------

// resetDetailsResponder wires a canned details payload with three rows whose
// expiry order differs from payload order: soonest-available must win every
// ordered surface (table, completion) regardless of arrival order.

// resetDetailsResponder wires a canned details payload with three rows whose
// expiry order differs from payload order: soonest-available must win every
// ordered surface (table, completion) regardless of arrival order.
func resetDetailsResponder(t *testing.T, e *quotaTestEnv) {
	t.Helper()
	e.respond("backend-api/wham/rate-limit-reset-credits", 200, `{
		"available_count": 2,
		"credits": [
			{"id":"credit-late-9999","title":"Later reset","status":"available","expires_at":"2099-06-01T00:00:00Z"},
			{"id":"credit-soon-1111","title":"Full reset","status":"available","expires_at":"2099-01-01T00:00:00Z"},
			{"id":"credit-used-0000","title":"Spent","status":"redeemed","expires_at":"2099-03-01T00:00:00Z"}
		]
	}`)
}

// TestQuotaReset_PrefixResolution pins /quota:reset:<partial> id resolution:
// an exact id wins, a UNIQUE prefix resolves, ambiguity is refused with the
// candidate list, unknown prefixes are refused, and with no cached details an
// explicit id passes through verbatim (the server validates it).

// TestQuotaReset_PrefixResolution pins /quota:reset:<partial> id resolution:
// an exact id wins, a UNIQUE prefix resolves, ambiguity is refused with the
// candidate list, unknown prefixes are refused, and with no cached details an
// explicit id passes through verbatim (the server validates it).
func TestQuotaReset_PrefixResolution(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	resetDetailsResponder(t, env)
	env.load(t)

	// Unique prefix resolves to the full cached credit.
	res := env.evalJSONObj(t, `resolveCreditId("credit-late")`)
	if res["error"] != nil || res["credit"] == nil {
		t.Fatalf("unique prefix must resolve, got %v", res)
	}

	// Exact id wins even when it is also a prefix of itself only.
	res = env.evalJSONObj(t, `resolveCreditId("credit-late-9999")`)
	if res["credit"] == nil {
		t.Fatalf("exact id must resolve, got %v", res)
	}

	// Unknown prefix refuses with guidance.
	out := env.callCommand("quota", "reset", "nope-nope")
	if !strings.Contains(out, "No available reset credit matches") {
		t.Fatalf("unknown prefix output = %q", out)
	}

	// No cached details → passthrough ({}), so explicit ids still reach the
	// server for validation instead of being locally rejected.
	env.evalJS(t, `delete _cache.codex.details`)
	res = env.evalJSONObj(t, `resolveCreditId("whatever-id")`)
	if len(res) != 0 {
		t.Fatalf("missing details must pass through unresolved, got %v", res)
	}

	// Ambiguity: two credits sharing a prefix must refuse with both listed.
	// Injected post-refresh (a real /quota:reset would overwrite the cache,
	// so the resolution helper itself pins this branch — the command returns
	// res.error verbatim, proven by the unknown-prefix case above).
	env.evalJS(t, `_cache.codex.details = {availableCount: 2, credits: [
		{id:"dup-aaaa-1", title:"One", status:"available", expiresAtMs: 4000000000000},
		{id:"dup-aaaa-2", title:"Two", status:"available", expiresAtMs: 3000000000000}
	]}`)
	res = env.evalJSONObj(t, `resolveCreditId("dup-aaaa")`)
	errMsg, _ := res["error"].(string)
	if errMsg == "" {
		t.Fatalf("ambiguous prefix must produce an error, got %v", res)
	}
	for _, want := range []string{"matches more than one", "dup-aaaa-1", "dup-aaaa-2"} {
		if !strings.Contains(errMsg, want) {
			t.Fatalf("ambiguous prefix error missing %q:\n%s", want, errMsg)
		}
	}
}

// TestQuotaResets_CompletionOffersCreditsByExpiry pins the reset-credit
// completions: typing "/quota:r" surfaces `reset:<full-id>` candidates built
// from the CACHED available credits ordered soonest-expiry-first (redeemed
// ones never offered), bare "/quota:" stays free of them, and the nested
// "reset:" level completes bare ids with the same order.

// TestQuotaResets_CompletionOffersCreditsByExpiry pins the reset-credit
// completions: typing "/quota:r" surfaces `reset:<full-id>` candidates built
// from the CACHED available credits ordered soonest-expiry-first (redeemed
// ones never offered), bare "/quota:" stays free of them, and the nested
// "reset:" level completes bare ids with the same order.
func TestQuotaResets_CompletionOffersCreditsByExpiry(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	resetDetailsResponder(t, env)
	env.load(t)
	fn := quotaCompleter(t, env)

	assertRCompletionOffersAvailable(t, fn)
	assertBareAndNestedCompletion(t, fn)
}

// assertRCompletionOffersAvailable checks that "/quota:r" offers reset:<id>
// candidates from CACHED available credits soonest-expiry-first and never
// surfaces redeemed credits.
func assertRCompletionOffersAvailable(t *testing.T, fn func(prefix string) []Completion) {
	t.Helper()
	r := completionValues(fn("r"))
	soon, late := indexOfString(r, "reset:credit-soon-1111"), indexOfString(r, "reset:credit-late-9999")
	if soon == -1 || late == -1 {
		t.Fatalf("'r' completion must offer reset:<available-id>, got %v", r)
	}
	if hasString(r, "credit-used-0000") || hasString(r, "reset:credit-used-0000") {
		t.Fatalf("'r' completion must not offer non-available credits, got %v", r)
	}
	if soon > late {
		t.Fatalf("soonest-expiry credit must sort first, got %v", r)
	}
}

// assertBareAndNestedCompletion checks that the bare "/quota:" level stays
// colon-free while the nested "reset:" level completes bare ids with the same
// soonest-first order and non-empty descriptions.
func assertBareAndNestedCompletion(t *testing.T, fn func(prefix string) []Completion) {
	t.Helper()
	for _, v := range completionValues(fn("")) {
		if strings.Contains(v, ":") {
			t.Fatalf("bare completion must stay colon-free, got %q", v)
		}
	}
	nested := completionValues(fn("reset:"))
	want := []string{"credit-soon-1111", "credit-late-9999"}
	if !slices.Equal(nested, want) {
		t.Fatalf("reset: level = %v, want %v", nested, want)
	}
	filtered := completionValues(fn("reset:credit-l"))
	if w2 := want[1:]; !slices.Equal(filtered, w2) {
		t.Fatalf("reset:credit-l level = %v, want %v", filtered, w2)
	}
	for _, c := range fn("reset:") {
		if c.Description == "" {
			t.Fatalf("reset credit completion %q lacks description", c.Value)
		}
	}
}
