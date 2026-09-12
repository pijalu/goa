// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/protocol"
)

// Regression for bugs-20260912-inferx-reasoning-effort: a user-configured
// thinking_level of xhigh on a custom provider (no variant profile, no
// ThinkingLevelMap) was silently downgraded to "medium" by
// ClampThinkingLevelWithMap. InferX's DeepSeek V4.1 endpoint rejects
// "medium" with HTTP 400 ("reasoning_effort must be low, high, xhigh, max,
// or an integer within [1, 100]"), so every turn failed. The test walks the
// exact production chain: config level → SimpleStreamOptions →
// BuildSimpleOptions → protocol request body.
func TestBuildSimpleOptions_ConfiguredXHighReachesWire(t *testing.T) {
	model := Model{
		ID:       "deepseek-v4.1-flash",
		Provider: "inferx", // user-defined provider: DefaultProfile, ThinkingFormat "openai"
		Api:      ApiOpenAICompletions,
		Reasoning: true, // set by manager_resolve.go because thinking_level != ""
	}
	// applyModelStreamOptions passes config thinking_level through raw.
	resolved := BuildSimpleOptions(model, SimpleStreamOptions{Reasoning: ThinkingXHigh})
	if resolved.Reasoning != ThinkingXHigh {
		t.Fatalf("configured xhigh downgraded to %q", resolved.Reasoning)
	}

	p := protocol.ForAPI(model.Api)
	if p == nil {
		t.Fatal("no protocol for openai-completions")
	}
	body, err := p.BuildRequest(model, Context{}, resolved, ResolveProfile(model))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if got, ok := req["reasoning_effort"].(string); !ok || got != "xhigh" {
		t.Errorf("wire reasoning_effort = %v, want xhigh", req["reasoning_effort"])
	}
}

// Same chain with a requested "max": must survive both the clamp and the
// map-based nearest-level path.
func TestBuildSimpleOptions_ConfiguredMaxReachesWire(t *testing.T) {
	model := Model{
		ID:        "deepseek-v4.1-flash",
		Provider:  "inferx",
		Api:       ApiOpenAICompletions,
		Reasoning: true,
	}
	resolved := BuildSimpleOptions(model, SimpleStreamOptions{Reasoning: ThinkingMax})
	if resolved.Reasoning != ThinkingMax {
		t.Fatalf("configured max downgraded to %q", resolved.Reasoning)
	}

	p := protocol.ForAPI(model.Api)
	body, err := p.BuildRequest(model, Context{}, resolved, ResolveProfile(model))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if got, ok := req["reasoning_effort"].(string); !ok || got != "max" {
		t.Errorf("wire reasoning_effort = %v, want max", req["reasoning_effort"])
	}
}
