// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// End-to-end regression (bug: temperature 400): the real kimi-code variant
// profile must resolve to SupportsTemperature=false, so a gemma model's
// temperature is omitted from the request body instead of rejected by the
// endpoint with HTTP 400 "invalid temperature: only 1 is allowed".
func TestKimiCodeProfileDisablesTemperature(t *testing.T) {
	model := schema.Model{ID: "google/gemma-4-e4b", Api: schema.ApiOpenAICompletions, Provider: "kimi-code"}
	profile := schema.ResolveProfile(model)
	t.Logf("resolved profile id=%q compat.SupportsTemperature=%v", profile.ID, profile.Compat.SupportsTemperature)
	if profile.Compat.SupportsTemperature == nil || *profile.Compat.SupportsTemperature != false {
		t.Errorf("kimi-code profile SupportsTemperature=%v, want explicit false", profile.Compat.SupportsTemperature)
	}
	temp := 0.2
	compat := resolveOpenAICompat(model, profile)
	body := buildOpenAIParams(model, schema.Context{}, schema.StreamOptions{Temperature: &temp}, profile, compat)
	if _, present := body["temperature"]; present {
		t.Errorf("temperature present for kimi-code model: %v", body["temperature"])
	}
}
