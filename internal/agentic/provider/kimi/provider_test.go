// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package kimi

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestKimiProvider_API(t *testing.T) {
	p := &KimiProvider{}
	if p.API() != provider.ApiOpenAICompletions {
		t.Errorf("API() = %q, want %q", p.API(), provider.ApiOpenAICompletions)
	}
}

func TestKimiExtras_ThinkingExtraBody(t *testing.T) {
	model := provider.Model{
		ID:       "kimi-k2.6",
		Provider: provider.ProviderKimi,
		Extra: map[string]interface{}{
			"thinking_extra_body": true,
		},
	}
	model = applyKimiExtras(model)
	compat, ok := model.Compat.(map[string]interface{})
	if !ok {
		t.Fatal("Compat should be a map after applyKimiExtras")
	}
	if v, ok := compat["thinking_extra_body"]; !ok || v != true {
		t.Errorf("thinking_extra_body not set in compat, got %v", compat)
	}
}

func TestKimiExtras_NormalizeNullDescriptions(t *testing.T) {
	model := provider.Model{
		ID:       "kimi-k2.6",
		Provider: provider.ProviderKimi,
		Extra: map[string]interface{}{
			"normalize_null_descriptions": true,
		},
	}
	model = applyKimiExtras(model)
	compat, ok := model.Compat.(map[string]interface{})
	if !ok {
		t.Fatal("Compat should be a map after applyKimiExtras")
	}
	if v, ok := compat["normalize_null_descriptions"]; !ok || v != true {
		t.Errorf("normalize_null_descriptions not set in compat, got %v", compat)
	}
}

func TestKimiExtras_ToolCallIDMaxLength(t *testing.T) {
	model := provider.Model{
		ID:       "kimi-k2.6",
		Provider: provider.ProviderKimi,
		Extra: map[string]interface{}{
			"tool_call_id_max_length": float64(64),
		},
	}
	model = applyKimiExtras(model)
	compat, ok := model.Compat.(map[string]interface{})
	if !ok {
		t.Fatal("Compat should be a map after applyKimiExtras")
	}
	v, ok := compat["tool_call_id_max_length"]
	if !ok {
		t.Fatal("tool_call_id_max_length not set in compat")
	}
	if vi, ok := v.(int); !ok || vi != 64 {
		t.Errorf("tool_call_id_max_length = %v (type %T), want 64", v, v)
	}
}

func TestKimiExtras_NoExtra(t *testing.T) {
	model := provider.Model{
		ID:       "test",
		Provider: provider.ProviderKimi,
	}
	model = applyKimiExtras(model)
	if model.Compat != nil {
		t.Errorf("Compat should be nil when no extras, got %v", model.Compat)
	}
}

func TestKimiExtras_ExtraNilCompat(t *testing.T) {
	model := provider.Model{
		ID:       "test",
		Provider: provider.ProviderKimi,
		Extra: map[string]interface{}{
			"thinking_extra_body": true,
		},
		Compat: nil,
	}
	model = applyKimiExtras(model)
	if model.Compat == nil {
		t.Fatal("Compat should be set after applyKimiExtras")
	}
}
