// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"reflect"
	"testing"
)

// schemaWithUnsupported builds a schema exercising every projected keyword:
// additionalProperties, a nullable anyOf union, and a nested single-variant
// anyOf that must flatten.
func schemaWithUnsupported() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":                 "string",
				"additionalProperties": false,
			},
			"count": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "integer"},
					map[string]any{"type": "null"},
				},
			},
			"nested": map[string]any{
				"anyOf": []any{
					map[string]any{
						"type":                 "object",
						"additionalProperties": false,
					},
				},
			},
		},
		"additionalProperties": false,
	}
}

func TestProjectToolSchema_OpenAIIsIdentity(t *testing.T) {
	model := Model{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", Api: ApiOpenAICompletions}
	input := schemaWithUnsupported()
	got := ProjectToolSchema(model, input)
	if !reflect.DeepEqual(got, input) {
		t.Errorf("OpenAI-family projection must be identity, got:\n%#v\nwant:\n%#v", got, input)
	}
}

func TestProjectToolSchema_GeminiDropsUnsupported(t *testing.T) {
	model := Model{Provider: ProviderGoogle, BaseURL: "https://generativelanguage.googleapis.com/v1beta", Api: ApiGoogleGenerativeAI}
	got := ProjectToolSchema(model, schemaWithUnsupported())

	if _, ok := got["additionalProperties"]; ok {
		t.Errorf("Gemini projection must drop top-level additionalProperties")
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties lost: %#v", got)
	}
	if _, ok := props["name"].(map[string]any)["additionalProperties"]; ok {
		t.Errorf("Gemini projection must drop nested additionalProperties")
	}
	// count: nullable union stripped + single variant flattened.
	count, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatalf("count lost: %#v", props)
	}
	if _, ok := count["anyOf"]; ok {
		t.Errorf("single-variant anyOf must flatten, count = %#v", count)
	}
	if typ, _ := count["type"].(string); typ != "integer" {
		t.Errorf("flattened count type = %q, want integer", typ)
	}
	// nested: single-variant anyOf with additionalProperties flattened + dropped.
	nested, ok := props["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested lost: %#v", props)
	}
	if _, ok := nested["anyOf"]; ok {
		t.Errorf("nested single-variant anyOf must flatten")
	}
	if _, ok := nested["additionalProperties"]; ok {
		t.Errorf("nested additionalProperties must drop after flatten")
	}
	if typ, _ := nested["type"].(string); typ != "object" {
		t.Errorf("nested flattened type = %q, want object", typ)
	}
}

func TestProjectToolSchema_MoonshotDropsUnsupported(t *testing.T) {
	model := Model{Provider: ProviderKimi, BaseURL: "https://api.moonshot.cn/v1", Api: ApiOpenAICompletions}
	got := ProjectToolSchema(model, schemaWithUnsupported())

	if _, ok := got["additionalProperties"]; ok {
		t.Errorf("Moonshot projection must drop additionalProperties")
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties lost: %#v", got)
	}
	count, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatalf("count lost: %#v", props)
	}
	if _, ok := count["anyOf"]; ok {
		t.Errorf("single-variant anyOf must flatten, count = %#v", count)
	}
	if typ, _ := count["type"].(string); typ != "integer" {
		t.Errorf("flattened count type = %q, want integer", typ)
	}
}

func TestToolSchemaFamilyForModel(t *testing.T) {
	cases := []struct {
		name  string
		model Model
		want  ToolSchemaFamily
	}{
		{"openai", Model{Provider: ProviderOpenAI, Api: ApiOpenAICompletions, BaseURL: "https://api.openai.com/v1"}, ToolSchemaFamilyOpenAI},
		{"gemini", Model{Provider: ProviderGoogle, Api: ApiGoogleGenerativeAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta"}, ToolSchemaFamilyGemini},
		{"vertex", Model{Provider: ProviderGoogle, Api: ApiGoogleVertex, BaseURL: "https://googleapis.com"}, ToolSchemaFamilyGemini},
		{"moonshot", Model{Provider: ProviderKimi, Api: ApiOpenAICompletions, BaseURL: "https://api.moonshot.cn/v1"}, ToolSchemaFamilyMoonshot},
		{"moonshot-url-only", Model{Provider: ProviderCustom, Api: ApiOpenAICompletions, BaseURL: "https://api.moonshot.ai/v1"}, ToolSchemaFamilyMoonshot},
		{"anthropic", Model{Provider: ProviderAnthropic, Api: ApiAnthropicMessages, BaseURL: "https://api.anthropic.com/v1"}, ToolSchemaFamilyOpenAI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolSchemaFamilyForModel(tc.model); got != tc.want {
				t.Errorf("ToolSchemaFamilyForModel(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestProjectToolSchema_DoesNotMutateInput(t *testing.T) {
	model := Model{Provider: ProviderGoogle, Api: ApiGoogleGenerativeAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta"}
	input := schemaWithUnsupported()
	before := schemaWithUnsupported()
	ProjectToolSchema(model, input)
	if !reflect.DeepEqual(input, before) {
		t.Errorf("projection must not mutate the input schema")
	}
}
