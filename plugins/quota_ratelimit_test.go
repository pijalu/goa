// SPDX-License-Identifier: GPL-3.0-or-later

package plugins

import (
	"testing"
	"time"
)

// waitFor polls a condition until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// rateLimitPayload builds a rate_limit_exceeded payload like the app-level
// forwarder sends onto the plugin bus.
func rateLimitPayload(model string) map[string]interface{} {
	return map[string]interface{}{
		"provider":       "openai",
		"model":          model,
		"attempt":        0,
		"retry_after_ms": int64(1500),
		"classified":     "rate_limit",
		"will_retry":     true,
	}
}

// TestQuota_RateLimitObserver_RefreshesAndHints drives §6 step 4 end-to-end:
// a codex-model rate_limit_exceeded event must force-refresh the codex entry
// and surface one hint telling the user how many reset credits exist.
func TestQuota_RateLimitObserver_RefreshesAndHints(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	bridge := env.load(t)
	if bridge == nil {
		t.Fatal("no bridge")
	}
	if len(env.observers) == 0 {
		t.Fatal("plugin did not register an observer for rate-limit events")
	}

	cap := startCapture(t, env)
	env.emitPluginEvent("rate_limit_exceeded", rateLimitPayload("gpt-5-codex"))

	// The observer defers its work to a 0-delay timer; wait until the forced
	// refresh has landed resetsCount in the cache.
	waitFor(t, "codex cache refreshed with resetsCount", func() bool {
		return env.evalJSBool(t, `_cache.codex && _cache.codex.resetsCount === 2`)
	})
	waitFor(t, "rate-limit reset hint", func() bool {
		return env.outputCount("/quota:resets") > 0
	})

	if got := env.outputCount("You have 2 rate-limit resets available"); got != 1 {
		t.Errorf("expected exactly 1 hint mentioning 2 resets, got %d", got)
	}
	if got := cap.getsTo("wham/usage"); got < 1 {
		t.Errorf("expected the forced refresh to GET usage, got %d calls", got)
	}
}

// TestQuota_RateLimitObserver_HintDebounce verifies the ≥10-minute debounce:
// repeated events force-refresh every time but surface at most one hint per
// window.
func TestQuota_RateLimitObserver_HintDebounce(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)

	cap := startCapture(t, env)
	env.emitPluginEvent("rate_limit_exceeded", rateLimitPayload("gpt-5-codex"))
	waitFor(t, "first refresh + hint", func() bool {
		return env.outputCount("/quota:resets") > 0
	})

	// A second burst of failures inside the debounce window: each event still
	// force-refreshes, but no additional hint may appear.
	env.emitPluginEvent("rate_limit_exceeded", rateLimitPayload("gpt-5-codex"))
	waitFor(t, "second forced refresh", func() bool {
		return cap.getsTo("wham/usage") >= 2
	})
	time.Sleep(100 * time.Millisecond) // settle: let any (wrong) extra timer run

	if got := env.outputCount("/quota:resets"); got != 1 {
		t.Errorf("debounce failed: expected exactly 1 hint, got %d", got)
	}
}

// TestQuota_RateLimitObserver_IgnoresNonCodexModels verifies non-Codex models
// trigger neither a fetch nor an output, and that a nil payload is tolerated.
func TestQuota_RateLimitObserver_IgnoresNonCodexModels(t *testing.T) {
	env := newQuotaTestEnv(t)
	wireCodexAuth(t, env)
	env.load(t)

	cap := startCapture(t, env)
	env.emitPluginEvent("rate_limit_exceeded", rateLimitPayload("claude-sonnet-4-5"))
	env.emitPluginEvent("rate_limit_exceeded", nil)
	time.Sleep(100 * time.Millisecond) // settle: timers would have fired

	if got := cap.getsTo("wham/usage"); got != 0 {
		t.Errorf("non-codex model must not trigger a refresh, got %d GETs", got)
	}
	if got := env.outputCount("/quota:resets"); got != 0 {
		t.Errorf("non-codex model must not produce a hint, got %d outputs", got)
	}
}
