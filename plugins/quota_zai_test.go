// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestQuota_ZaiCodingAliasResolvesZaiFetcher verifies that a provider config
// using a zai-coding* identity (e.g. id "zai-coding", provider "zai-coding")
// still resolves to the "zai" quota fetcher via fetcherAliases — the same
// monitor API serves the GLM Coding Plan regardless of the config alias.
func TestQuota_ZaiCodingAliasResolvesZaiFetcher(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai-coding", map[string]any{"provider": "zai-coding", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai-coding")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":41,"nextResetTime":1784656400096}]}}`)
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, "41") {
		t.Fatalf("segment should show the zai-coding provider quota via the zai fetcher: %q", seg)
	}
}

// TestQuota_ZaiEndpointWithPathStillHitsMonitorHost is the regression for
// "z.ai is not visible in quota": when the provider config carries the full
// inference endpoint (…/api/coding/paas/v4) in baseUrl or endpoint, the
// fetcher must strip to the API host before appending the monitor route —
// otherwise the monitor URL 404s and the provider vanishes from /quota.
func TestQuota_ZaiEndpointWithPathStillHitsMonitorHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  map[string]any
	}{
		{"baseUrl with path", map[string]any{"provider": "zai", "apiKey": "k", "baseUrl": "https://api.z.ai/api/coding/paas/v4"}},
		{"endpoint with path", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"}},
		{"CN endpoint with path", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://open.bigmodel.cn/api/coding/paas/v4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newQuotaTestEnv(t)
			env.setProvider("zai", tc.cfg)
			env.setActiveProvider("zai")
			host := "api.z.ai"
			if strings.Contains(fmt.Sprint(tc.cfg["endpoint"]), "bigmodel") {
				host = "open.bigmodel.cn"
			}
			env.respond(host+"/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":38,"nextResetTime":1784656400096}]}}`)
			env.load(t)
			env.callCommand("quota", "refresh")
			seg := env.renderSegment()
			if !strings.Contains(seg, "38") {
				t.Fatalf("segment should show quota via the monitor host %s: %q", host, seg)
			}
		})
	}
}

// TestQuota_SegmentTracksActiveProviderOnly pins the status-bar contract:
// with several providers holding quota data, the segment shows ONLY the
// active provider — no rotation, no other provider names.
func TestQuota_SegmentTracksActiveProviderOnly(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.setProvider("z.ai", map[string]any{"provider": "zai", "apiKey": "k"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":38,"nextResetTime":1784656400096}]}}`)
	env.setActiveProvider("z.ai")
	env.load(t)
	env.callCommand("quota", "refresh")
	seg := env.renderSegment()
	if !strings.Contains(seg, "38") {
		t.Fatalf("segment should show the active provider (z.ai): %q", seg)
	}
	if strings.Contains(seg, "42") || strings.Contains(seg, "Anthropic") {
		t.Fatalf("segment leaked the inactive provider: %q", seg)
	}
	// Bracketed compact form (ANSI-stripped: the segment may carry a color).
	stripped := ansi.Strip(seg)
	if !strings.HasPrefix(stripped, "[") || !strings.HasSuffix(stripped, "]") {
		t.Fatalf("segment should be bracketed: %q", stripped)
	}
}

// TestQuota_ZaiAppearsInFullQuotaOutput is the regression for "z.ai does not
// appear in /quota either": with a working key and monitor API, the z.ai
// provider must render a row in the full /quota command output (not just the
// footer segment).
func TestQuota_ZaiAppearsInFullQuotaOutput(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":41,"nextResetTime":1784656400096}]}}`)
	env.load(t)
	env.warmCache(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai") {
		t.Fatalf("/quota output must contain the Z.ai provider row, got:\n%s", out)
	}
	if !strings.Contains(out, "41") {
		t.Fatalf("/quota output must contain the z.ai usage figure 41, got:\n%s", out)
	}
}

// TestQuota_ZaiKeylessShowsNoAPIKeyRow verifies a configured-but-keyless z.ai
// provider surfaces a "no API key" status row in /quota instead of vanishing
// silently (bugs.md: z.ai not visible in /quota).
func TestQuota_ZaiKeylessShowsNoAPIKeyRow(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai", map[string]any{"provider": "zai", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai")
	env.load(t)
	env.warmCache(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai") {
		t.Fatalf("/quota output must contain the Z.ai provider row even when keyless, got:\n%s", out)
	}
	if !strings.Contains(out, "no API key") {
		t.Fatalf("/quota output must explain the missing key, got:\n%s", out)
	}
}

// TestQuota_ZaiIdOnlyConfigAppearsInFullQuotaOutput is the regression for the
// 2026-07-21 re-report: the user's real config entry has `id: zai`, an
// endpoint and an api_key but NO `provider:` identity field (unlike sibling
// entries). The z.ai row must still appear in /quota output.
func TestQuota_ZaiIdOnlyConfigAppearsInFullQuotaOutput(t *testing.T) {
	env := newQuotaTestEnv(t)
	// Exact user config shape from bugs.md: id only, no `provider:` key.
	env.setProvider("zai", map[string]any{"id": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("kimi-code")
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":41,"nextResetTime":1784656400096}]}}`)
	env.load(t)
	env.warmCache(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai") {
		t.Fatalf("/quota output must contain the Z.ai provider row for an id-only config, got:\n%s", out)
	}
	if !strings.Contains(out, "41") {
		t.Fatalf("/quota output must contain the z.ai usage figure 41, got:\n%s", out)
	}
}

// TestQuota_ZaiRealMonitorResponseShape is the regression for "z.ai not
// showing quota (fix didn't work)": the live z.ai monitor API returns
// data.limits[] as an ARRAY of typed windows (TIME_LIMIT / TOKENS_LIMIT with
// percentage + nextResetTime), NOT the keyed {session, weekly} object the
// generic mapper expected — which produced zero limits and made the Z.ai row
// vanish. The zai fetcher must parse the real array shape into quota rows.
func TestQuota_ZaiRealMonitorResponseShape(t *testing.T) {
	env := newQuotaTestEnv(t)
	env.setProvider("zai", map[string]any{"provider": "zai", "apiKey": "k", "endpoint": "https://api.z.ai/api/coding/paas/v4"})
	env.setActiveProvider("zai")
	// Exact shape returned by https://api.z.ai/api/monitor/usage/quota/limit.
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"code":200,"data":{"level":"pro","limits":[
		{"type":"TIME_LIMIT","unit":5,"number":1,"usage":1000,"currentValue":0,"remaining":1000,"percentage":0,"nextResetTime":1787122604987},
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":41,"nextResetTime":1784656400096},
		{"type":"TOKENS_LIMIT","unit":6,"number":7,"percentage":38,"nextResetTime":1784876204993}
	]},"success":true}`)
	env.load(t)
	env.warmCache(t)
	out := env.callCommand("quota")
	if !strings.Contains(out, "Z.ai") {
		t.Fatalf("Z.ai row missing for the real monitor response shape:\n%s", out)
	}
	if !strings.Contains(out, "41%") {
		t.Fatalf("z.ai session window (41%%) missing:\n%s", out)
	}
	if !strings.Contains(out, "38%") {
		t.Fatalf("z.ai weekly window (38%%) missing:\n%s", out)
	}
	// Footer segment must show the active provider's quota, not vanish.
	seg := env.renderSegment()
	if seg == "" {
		t.Fatalf("z.ai footer segment must render for the active provider, got empty")
	}
}
