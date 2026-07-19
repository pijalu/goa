// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ANSI RGB triplets produced by the harness SegmentColor hexes.
const (
	rgbOk       = "38;2;63;185;80"  // #3fb950
	rgbWarn     = "38;2;210;153;34" // #d29922
	rgbCritical = "38;2;248;81;73"  // #f85149
	rgbPending  = "38;2;139;148;158" // #8b949e
)

// kimiBody builds a /coding/v1/usages payload with a 5h session window whose
// reset is `resetIn` from now and `usedPct`% of the limit consumed.
func kimiBody(usedPct int, resetIn time.Duration) string {
	reset := time.Now().Add(resetIn).UTC().Format(time.RFC3339)
	weeklyReset := time.Now().Add(96 * time.Hour).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{
		"user": {"membership": {"level": "LEVEL_ADVANCED"}},
		"usage": {"limit": "100", "used": "10", "resetTime": %q},
		"limits": [
			{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			 "detail": {"limit": "100", "used": "%d", "resetTime": %q}}
		]
	}`, weeklyReset, usedPct, reset)
}

// setupKimiActive wires a kimi-code active provider with the given API body
// and returns the rendered segment after a refresh.
func setupKimiSegment(t *testing.T, body string) string {
	t.Helper()
	env := newQuotaTestEnv(t)
	env.setProvider("kimi-code", map[string]any{"provider": "kimi-code", "apiKey": "sk", "endpoint": "https://api.kimi.com/coding/v1"})
	env.setActiveProvider("kimi-code")
	env.respond("api.kimi.com/coding/v1/usages", 200, body)
	env.load(t)
	env.callCommand("quota", "refresh")
	return env.renderSegment()
}

// TestQuota_SegmentColorInBudget: 10% used with only ~20% of the window
// elapsed projects ~50% window-end usage → green (ok).
func TestQuota_SegmentColorInBudget(t *testing.T) {
	seg := setupKimiSegment(t, kimiBody(10, 4*time.Hour)) // reset in 4h of a 5h window
	if !strings.Contains(seg, rgbOk) {
		t.Fatalf("in-budget segment should be green, got: %q", seg)
	}
	// Compact form: session|weekly percentages without window labels.
	if !strings.Contains(seg, "[10%|") {
		t.Fatalf("segment missing quota text: %q", seg)
	}
}

// TestQuota_SegmentColorClose: 70% used with 80% of the window elapsed
// projects ~87% window-end usage → orange (warn).
func TestQuota_SegmentColorClose(t *testing.T) {
	seg := setupKimiSegment(t, kimiBody(70, 1*time.Hour)) // reset in 1h of a 5h window
	if !strings.Contains(seg, rgbWarn) {
		t.Fatalf("close segment should be orange, got: %q", seg)
	}
}

// TestQuota_SegmentColorOverrun: 90% used with 80% of the window elapsed
// projects ~112% window-end usage → red (critical).
func TestQuota_SegmentColorOverrun(t *testing.T) {
	seg := setupKimiSegment(t, kimiBody(90, 1*time.Hour))
	if !strings.Contains(seg, rgbCritical) {
		t.Fatalf("overrun segment should be red, got: %q", seg)
	}
}

// TestQuota_SegmentColorPendingNoData: no fetch has completed yet, so the
// segment shows the white/pending placeholder instead of a misleading 0%.
func TestQuota_SegmentColorPendingNoData(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("kimi-code", map[string]any{"provider": "kimi-code", "apiKey": "sk", "endpoint": "https://api.kimi.com/coding/v1"})
	env.setActiveProvider("kimi-code")
	// Slow responder never registered → every fetch 404s, but even before
	// that: render immediately after load with no refresh.
	env.load(t)
	// Note: load primes the cache via refreshAllDue; with no responder the
	// fetch errors (http_404), so the segment shows the warn marker, NOT a
	// bogus "0%" — either way it must not claim a percentage.
	seg := env.renderSegment()
	if strings.Contains(seg, "%]") {
		t.Fatalf("segment must not show a percentage before any data: %q", seg)
	}
}

// TestQuota_SegmentColorLocalInfinite: the local provider has no quota limit,
// so it shows the infinite symbol in green rather than a percentage.
func TestQuota_SegmentColorLocalInfinite(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Active provider "local" maps to the local fetcher via fallback id.
	env.setActiveProvider("local")
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, rgbOk) {
		t.Fatalf("local segment should be green/ok, got: %q", seg)
	}
	if !strings.Contains(seg, "∞") {
		t.Fatalf("local segment should show the infinite symbol, got: %q", seg)
	}
}
