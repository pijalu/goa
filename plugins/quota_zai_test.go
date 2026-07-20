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
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":41,"limit":100}}}`)
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
			env.respond(host+"/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
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
	env.respond("api.z.ai/api/monitor/usage/quota/limit", 200, `{"data":{"session":{"used":38,"limit":100}}}`)
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
