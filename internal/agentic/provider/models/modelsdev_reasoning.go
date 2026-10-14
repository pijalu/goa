// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"bytes"
	"encoding/json"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// models.dev publishes more about a model's reasoning than the boolean flag:
// the values its effort control accepts, and the field it wants reasoning
// echoed in on assistant messages. This file decodes that metadata and derives
// the registry fields from it (thinking level map, thinking format), keeping
// the catalog shape and its consumers together.
//
// Decoding is deliberately lenient: the catalog is upstream data, and one
// unrecognized shape must not take a whole provider's models with it.

// mdReasoningOption is one entry of models.dev "reasoning_options". Unknown
// keys and non-string values are dropped instead of failing the decode.
type mdReasoningOption struct {
	Type   string
	Values []string
}

// UnmarshalJSON reads the option leniently: the type is always a string, and
// values are kept when they decode as strings.
func (o *mdReasoningOption) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type   string            `json:"type"`
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	o.Type = raw.Type
	for _, v := range raw.Values {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			continue
		}
		o.Values = append(o.Values, s)
	}
	return nil
}

// mdInterleaved is models.dev's "interleaved" value: a field descriptor or the
// bare boolean true. Both shapes decode (a bool simply names no field), and an
// unrecognized shape is ignored rather than failing the provider entry.
type mdInterleaved struct {
	Field string
}

// UnmarshalJSON accepts the object and boolean spellings of "interleaved".
func (m *mdInterleaved) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '{' {
		// true / false / any scalar: "interleaves, field unspecified".
		return nil
	}
	var raw struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil
	}
	m.Field = raw.Field
	return nil
}

// thinkingFormatFromInterleaved maps the catalog's "interleaved.field" — the
// field the model expects reasoning echoed in on assistant messages — to the
// thinking format Goa replays it as. Only names Goa has semantics for are
// mapped: "reasoning_details" (OpenRouter-style) has no parser or replay path
// here, and inventing one would change how thinking is replayed for 57 models
// nobody has validated. Silence means "no opinion", not "no reasoning".
func thinkingFormatFromInterleaved(iv *mdInterleaved) provider.ThinkingFormat {
	if iv == nil {
		return ""
	}
	if iv.Field == string(provider.ThinkingFormatReasoningContent) {
		return provider.ThinkingFormatReasoningContent
	}
	return ""
}

// reasoningOptionLevelMap turns the effort values a model declares into a
// thinking-level map with identity entries. The effect is clamping, not
// translation: a level the model does not list (an endpoint answering 400
// "reasoning_effort must be low, high, xhigh, max") resolves to the nearest one
// it does list, while a listed level keeps its own name. Curated
// model_overrides.yaml entries are applied on top and replace this map
// wholesale, so hand-verified provider semantics always win.
//
// Only canonical levels are mapped and only "effort" options are considered:
// "toggle" states on/off without naming levels, and "budget_tokens" belongs to
// the separate ThinkingBudgets domain (per-level token counts, not names).
func reasoningOptionLevelMap(options []mdReasoningOption, reasoning bool) provider.ThinkingLevelMap {
	if !reasoning {
		return nil
	}
	var out provider.ThinkingLevelMap
	for _, opt := range options {
		if opt.Type != "effort" {
			continue
		}
		for _, value := range opt.Values {
			level := provider.ThinkingLevel(value)
			if !canonicalThinkingLevels[level] {
				continue
			}
			if out == nil {
				out = provider.ThinkingLevelMap{}
			}
			out[level] = value
		}
	}
	return out
}

// canonicalThinkingLevels are the levels a caller can request. ThinkingOff is
// excluded: it is answered with the provider's disabled body, never with a
// mapped value.
var canonicalThinkingLevels = map[provider.ThinkingLevel]bool{
	provider.ThinkingMinimal: true,
	provider.ThinkingLow:     true,
	provider.ThinkingMedium:  true,
	provider.ThinkingHigh:    true,
	provider.ThinkingXHigh:   true,
	provider.ThinkingMax:     true,
}
