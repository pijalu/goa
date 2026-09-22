// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider_test

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/agentic/provider/protocol"
)

// Regression for goa-export-20260922-074352: a user-defined mimo-v2.6-flash on
// the opencode-go gateway was configured with thinking_level xhigh. The
// models.dev catalog entry carried no thinking_level_map, so
// ClampThinkingLevelWithMap passed xhigh straight through and deepseekThinking
// put reasoning_effort:"xhigh" on the wire. The upstream mimo endpoint rejects
// xhigh/max/minimal with HTTP 400 "Invalid request parameters" (verified live:
// only low/medium/high accepted), failing the very first request with
// "Error calling model".
//
// The fix is a curated mimo- prefix override (model_overrides.yaml) pinning a
// restricted thinking_level_map {off,low,medium,high}; the clamp downgrades
// xhigh to high before it reaches the wire. The user's xhigh preference is
// preserved in the UI; only the wire value is clamped. The test walks the real
// production chain: registry lookup (as ResolveActiveModel does) ->
// BuildSimpleOptions -> protocol request body.
func wireReasoningEffort(t *testing.T, m *provider.Model, level provider.ThinkingLevel) string {
	t.Helper()
	if m == nil {
		t.Fatal("model not found in registry")
	}
	resolved := provider.BuildSimpleOptions(*m, provider.SimpleStreamOptions{Reasoning: level})
	p := protocol.ForAPI(m.Api)
	if p == nil {
		t.Fatalf("no protocol for api %q", m.Api)
	}
	body, err := p.BuildRequest(*m, provider.Context{}, resolved, provider.ResolveProfile(*m))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	got, _ := req["reasoning_effort"].(string)
	return got
}

func TestMimoOpencodeGo_XHighClampedToHigh(t *testing.T) {
	m := models.GetModelForProvider(provider.Provider("opencode-go"), "mimo-v2.6-flash")
	if got := wireReasoningEffort(t, m, provider.ThinkingXHigh); got != "high" {
		t.Errorf("wire reasoning_effort = %q, want high (clamped from xhigh)", got)
	}
}

func TestMimoOpencodeGo_SupportedLevelPassesThrough(t *testing.T) {
	m := models.GetModelForProvider(provider.Provider("opencode-go"), "mimo-v2.6-flash")
	if got := wireReasoningEffort(t, m, provider.ThinkingMedium); got != "medium" {
		t.Errorf("wire reasoning_effort = %q, want medium (within allow-list)", got)
	}
}

// Max is also rejected by mimo upstream; it must clamp to high too.
func TestMimoOpencodeGo_MaxClampedToHigh(t *testing.T) {
	m := models.GetModelForProvider(provider.Provider("opencode-go"), "mimo-v2.6-flash")
	if got := wireReasoningEffort(t, m, provider.ThinkingMax); got != "high" {
		t.Errorf("wire reasoning_effort = %q, want high (clamped from max)", got)
	}
}

// The override is a family prefix: every mimo member on opencode-go is covered.
func TestMimoOpencodeGo_PrefixCoversFamily(t *testing.T) {
	for _, id := range []string{"mimo-v2.6-flash", "mimo-v2-pro", "mimo-v2.5-pro"} {
		m := models.GetModelForProvider(provider.Provider("opencode-go"), id)
		if m == nil {
			t.Errorf("%s: not found in registry", id)
			continue
		}
		if len(m.ThinkingLevelMap) == 0 {
			t.Errorf("%s: no thinking_level_map after override", id)
		}
	}
}
