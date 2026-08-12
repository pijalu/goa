// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
)

// Regression (bug: temperature 400): kimi-code rejects any temperature other
// than its fixed default (1) with HTTP 400 "invalid temperature: only 1 is
// allowed for this model". A model configured with temperature: 0.2 must not
// forward it: when the variant profile marks the provider as not supporting
// temperature, the request omits the field so the endpoint applies its own
// default instead of erroring the whole turn.
func TestBuildOpenAIParams_OmitsTemperatureWhenUnsupported(t *testing.T) {
	temp := 0.2
	model := schema.Model{ID: "google/gemma-4-e4b"}
	opts := schema.StreamOptions{Temperature: &temp}
	profile := schema.VariantProfile{
		Compat: schema.CompatFlags{SupportsTemperature: boolPtrLocal(false)},
	}
	compat := resolveOpenAICompat(model, profile)
	body := buildOpenAIParams(model, schema.Context{}, opts, profile, compat)
	_, present := body["temperature"]
	assert.False(t, present, "temperature must be omitted for a provider that does not support it (got %v)", body["temperature"])
}

// A standard provider (supports_temperature true / unset) keeps sending the
// configured temperature.
func TestBuildOpenAIParams_SendsTemperatureWhenSupported(t *testing.T) {
	temp := 0.2
	model := schema.Model{ID: "gpt-4o"}
	opts := schema.StreamOptions{Temperature: &temp}

	profiles := map[string]schema.VariantProfile{
		"explicit true": {Compat: schema.CompatFlags{SupportsTemperature: boolPtrLocal(true)}},
		"unset":         {},
	}
	for name, profile := range profiles {
		compat := resolveOpenAICompat(model, profile)
		body := buildOpenAIParams(model, schema.Context{}, opts, profile, compat)
		got, present := body["temperature"]
		assert.True(t, present, "%s: temperature must be sent for a provider that supports it", name)
		assert.Equal(t, temp, got, "%s: temperature value", name)
	}
}

func boolPtrLocal(v bool) *bool { return &v }
