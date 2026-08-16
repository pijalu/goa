// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// ---------------------------------------------------------------------------
// Tool schema projection (P6)
// ---------------------------------------------------------------------------

// ToolSchemaFamily identifies how a provider's tool input schemas must be
// projected for the wire. Providers differ in the JSON Schema dialect they
// accept for tool parameters; the projection normalizes the shared schema
// text for families that reject part of it (opencode tool-schema.ts parity).
type ToolSchemaFamily int

const (
	// ToolSchemaFamilyOpenAI accepts the full JSON Schema dialect goa emits
	// today (including additionalProperties and nullable unions). No
	// projection is applied: OpenAI-family requests stay byte-identical to
	// the historical output.
	ToolSchemaFamilyOpenAI ToolSchemaFamily = iota
	// ToolSchemaFamilyGemini is the Google Generative AI / Vertex dialect,
	// which rejects a subset of JSON Schema keywords in tool parameters.
	ToolSchemaFamilyGemini
	// ToolSchemaFamilyMoonshot is the Moonshot (Kimi) dialect, which also
	// rejects additionalProperties and nullable union arms.
	ToolSchemaFamilyMoonshot
)

// ToolSchemaFamilyForModel returns the projection family for a model. It is
// keyed on the API family (Gemini vs OpenAI-completions) with the existing
// compat fingerprint (compat_detect.go) separating Moonshot from the rest of
// the OpenAI-completions family.
func ToolSchemaFamilyForModel(model Model) ToolSchemaFamily {
	switch model.Api {
	case schema.ApiGoogleGenerativeAI, schema.ApiGoogleVertex:
		return ToolSchemaFamilyGemini
	case schema.ApiOpenAICompletions, schema.ApiOpenAIResponses,
		schema.ApiAzureOpenAIResponses, schema.ApiOpenAICodexResponses:
		if fingerprintProvider(model.Provider, model.BaseURL).isMoonshot {
			return ToolSchemaFamilyMoonshot
		}
	}
	return ToolSchemaFamilyOpenAI
}

// ProjectToolSchema normalizes a tool input schema for the model's provider
// family. The OpenAI family returns the input unchanged so wire requests stay
// byte-identical; the Gemini/Moonshot families drop the keywords they reject
// (additionalProperties), strip null union arms, and flatten a single
// remaining anyOf variant (opencode removeNullSchemas parity). The original
// map is never mutated.
func ProjectToolSchema(model Model, input map[string]any) map[string]any {
	switch ToolSchemaFamilyForModel(model) {
	case ToolSchemaFamilyGemini, ToolSchemaFamilyMoonshot:
		if projected, ok := projectToolSchemaNode(input).(map[string]any); ok {
			return projected
		}
		return input
	default:
		return input
	}
}

// projectToolSchemaNode recursively normalizes a schema node for providers
// that reject part of the JSON Schema dialect.
func projectToolSchemaNode(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return projectToolSchemaObject(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = projectToolSchemaNode(item)
		}
		return out
	default:
		return value
	}
}

// projectToolSchemaObject applies the per-object projection: drop
// additionalProperties, then normalize anyOf (strip null arms, flatten a
// single remaining variant into the parent).
func projectToolSchemaObject(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch k {
		case "additionalProperties":
			// Unsupported by the Gemini/Moonshot tool-schema dialect.
			continue
		case "anyOf":
			// Handled below, after the non-combiner keys are copied.
			continue
		default:
			out[k] = projectToolSchemaNode(v)
		}
	}
	return projectAnyOf(m, out)
}

// projectAnyOf normalizes the anyOf combiner of the source object into out:
// strip null union arms and flatten a single remaining variant into the
// parent. The source's other keys were already copied by the caller.
func projectAnyOf(src, out map[string]any) map[string]any {
	rawVariants, ok := src["anyOf"]
	if !ok {
		return out
	}
	variants, ok := rawVariants.([]any)
	if !ok {
		return out
	}
	kept := make([]any, 0, len(variants))
	for _, variant := range variants {
		if vm, ok := variant.(map[string]any); ok {
			if t, _ := vm["type"].(string); t == "null" {
				continue // strip null union arms
			}
		}
		kept = append(kept, projectToolSchemaNode(variant))
	}
	if len(kept) == 1 {
		// Flatten a single remaining variant into the parent.
		if flat, ok := kept[0].(map[string]any); ok {
			for k, v := range flat {
				out[k] = v
			}
			return out
		}
	}
	if len(kept) > 1 {
		out["anyOf"] = kept
	}
	return out
}
