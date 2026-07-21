// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"testing"
	"time"
)

// TestQuota_BareQuotaColdCacheReturnsImmediately is the regression for
// "Quota command unresponsive": a bare /quota with a cold cache (plugin just
// loaded, scheduler tick not yet landed) must return an immediate processing
// acknowledgment instead of blocking the input line on provider HTTP calls;
// the full table is emitted asynchronously via goa.output when fetches land.
func TestQuota_BareQuotaColdCacheReturnsImmediately(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":41,"limit":100}}}`)
	env.load(t)
	// load() drains the load-time prime (warm cache); force the cold-start
	// state the scenario needs by clearing the cache + fetch timestamps.
	env.evalJS(t, `_cache = {}; _lastFetch = {};`)

	// A bare /quota on a cold cache must acknowledge processing immediately
	// and fetch off the command path.
	out := env.callCommand("quota")
	if !strings.Contains(out, "Fetching quotas") {
		t.Fatalf("cold /quota must acknowledge processing immediately, got: %q", out)
	}
	if strings.Contains(out, "Provider Quotas") {
		t.Fatalf("cold /quota must not block to render the full table synchronously, got: %q", out)
	}

	// The async path emits the full table via goa.output once fetches land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(env.lastOutput(), "Provider Quotas") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	final := env.lastOutput()
	if !strings.Contains(final, "Z.ai") || !strings.Contains(final, "41") {
		t.Fatalf("async /quota output must contain the Z.ai row with usage 41, got: %q", final)
	}
}

// TestQuota_BareQuotaWarmCacheRendersInstantly verifies that with a warm
// cache (scheduler already fetched), a bare /quota renders synchronously from
// the cache without any fetching notice.
func TestQuota_BareQuotaWarmCacheRendersInstantly(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":41,"limit":100}}}`)
	env.load(t)

	// Warm the cache via an explicit forced refresh (synchronous).
	env.callCommand("quota", "refresh")

	out := env.callCommand("quota")
	if strings.Contains(out, "Fetching quotas") {
		t.Fatalf("warm /quota must render from cache, got the fetching notice: %q", out)
	}
	if !strings.Contains(out, "Z.ai") || !strings.Contains(out, "41") {
		t.Fatalf("warm /quota must contain the Z.ai row with usage 41, got: %q", out)
	}
}
