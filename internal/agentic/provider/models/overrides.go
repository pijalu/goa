// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
	"gopkg.in/yaml.v3"
)

// embeddedAPIJSON is a slimmed snapshot of models.dev/api.json committed to the
// repo. It serves as the no-internet fallback: the runtime catalog
// (modelsdev.go) refreshes it at startup, but the embed guarantees every
// provider and model is available even on first run with no network.
//
//go:embed api.json
var embeddedAPIJSON []byte

// embeddedOverridesYAML carries hand-curated behavioral data (thinking format,
// level maps, compat quirks) that models.dev cannot provide. Applied on top of
// the embedded api.json — overrides take priority over both the embedded
// catalog and the runtime catalog.
//
//go:embed model_overrides.yaml
var embeddedOverridesYAML []byte

// overrideModel mirrors the YAML structure of model_overrides.yaml. Only fields
// that differ from the models.dev defaults need to be set; the zero value means
// "inherit from the catalog entry."
type overrideModel struct {
	ID               string            `yaml:"id"`
	Provider         string            `yaml:"provider"`
	API              string            `yaml:"api,omitempty"`
	Name             string            `yaml:"name,omitempty"`
	Reasoning        *bool             `yaml:"reasoning,omitempty"`
	ContextWindow    *int              `yaml:"context_window,omitempty"`
	MaxTokens        *int              `yaml:"max_tokens,omitempty"`
	ThinkingFormat   string            `yaml:"thinking_format,omitempty"`
	ThinkingLevelMap map[string]string `yaml:"thinking_level_map,omitempty"`
	Compat           map[string]bool   `yaml:"compat,omitempty"`
	Cost             *struct {
		Input      *float64 `yaml:"input,omitempty"`
		Output     *float64 `yaml:"output,omitempty"`
		CacheRead  *float64 `yaml:"cache_read,omitempty"`
		CacheWrite *float64 `yaml:"cache_write,omitempty"`
	} `yaml:"cost,omitempty"`
	InputTypes []string `yaml:"input_types,omitempty"`

	// BaseURL overrides the provider's default endpoint (rarely needed; most
	// overrides rely on the provider identity for URL resolution).
	BaseURL string `yaml:"base_url,omitempty"`
}

type overrideFile struct {
	Models []overrideModel `yaml:"models"`
}

// loadEmbeddedCatalog parses the embedded api.json into model entries using the
// same two-pass logic as the runtime catalog (parseModelsDev): mapped providers
// first, then unmapped providers via the fallback. Returns the parsed models.
func loadEmbeddedCatalog() []provider.Model {
	cat, err := parseModelsDev(embeddedAPIJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goa: warning: failed to parse embedded api.json: %v\n", err)
		return nil
	}
	var out []provider.Model
	for _, models := range cat.byProv {
		out = append(out, models...)
	}
	return out
}

// loadOverrides parses the embedded YAML and returns the override entries.
func loadOverrides() []overrideModel {
	var f overrideFile
	if err := yaml.Unmarshal(embeddedOverridesYAML, &f); err != nil {
		fmt.Fprintf(os.Stderr, "goa: warning: failed to parse model_overrides.yaml: %v\n", err)
		return nil
	}
	return f.Models
}

// applyOverride merges an override entry onto a base model. Fields set in the
// override replace the base; unset fields are inherited.
func applyOverride(base provider.Model, ov overrideModel) provider.Model {
	m := base
	m.ID = coalesceStr(ov.ID, base.ID)
	m.Provider = coalesceProvider(ov.Provider, base.Provider)
	m.Api = coalesceAPI(ov.API, base.Api)
	m.Name = coalesceStr(ov.Name, base.Name)
	m.BaseURL = coalesceStr(ov.BaseURL, base.BaseURL)
	m.ThinkingFormat = coalesceThinkingFormat(ov.ThinkingFormat, base.ThinkingFormat)
	if ov.Reasoning != nil {
		m.Reasoning = *ov.Reasoning
	}
	if ov.ContextWindow != nil {
		m.ContextWindow = *ov.ContextWindow
	}
	if ov.MaxTokens != nil {
		m.MaxTokens = *ov.MaxTokens
	}
	if len(ov.ThinkingLevelMap) > 0 {
		m.ThinkingLevelMap = convertLevelMap(ov.ThinkingLevelMap)
	}
	if len(ov.InputTypes) > 0 {
		m.InputTypes = ov.InputTypes
	}
	if ov.Cost != nil {
		m.Cost = mergeCost(base.Cost, ov.Cost)
	}
	if len(ov.Compat) > 0 {
		m.Compat = buildCompatFromYAML(ov.Compat)
	}
	return m
}

func coalesceStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func coalesceProvider(val string, fallback provider.Provider) provider.Provider {
	if val != "" {
		return provider.Provider(val)
	}
	return fallback
}

func coalesceAPI(val string, fallback provider.Api) provider.Api {
	if val != "" {
		return provider.Api(val)
	}
	return fallback
}

func coalesceThinkingFormat(val string, fallback provider.ThinkingFormat) provider.ThinkingFormat {
	if val != "" {
		return provider.ThinkingFormat(val)
	}
	return fallback
}

func convertLevelMap(yamlMap map[string]string) provider.ThinkingLevelMap {
	out := provider.ThinkingLevelMap{}
	for k, v := range yamlMap {
		out[provider.ThinkingLevel(k)] = v
	}
	return out
}

func mergeCost(base provider.ModelPricing, c *struct {
	Input      *float64 `yaml:"input,omitempty"`
	Output     *float64 `yaml:"output,omitempty"`
	CacheRead  *float64 `yaml:"cache_read,omitempty"`
	CacheWrite *float64 `yaml:"cache_write,omitempty"`
}) provider.ModelPricing {
	p := base // start from the base so cache_read/write are preserved
	if c.Input != nil {
		p.Input = *c.Input
	}
	if c.Output != nil {
		p.Output = *c.Output
	}
	if c.CacheRead != nil {
		p.CacheRead = *c.CacheRead
	}
	if c.CacheWrite != nil {
		p.CacheWrite = *c.CacheWrite
	}
	return p
}

// buildCompatFromYAML converts the YAML compat map (snake_case keys → bool) into
// an OpenAICompletionsCompat struct. Only fields present in the struct are
// mapped; unknown keys are silently ignored.
func buildCompatFromYAML(flags map[string]bool) provider.OpenAICompletionsCompat {
	c := provider.OpenAICompletionsCompat{}
	for k, v := range flags {
		ptr := provider.BoolPtr(v)
		switch k {
		case "requires_reasoning_content_on_assistant_messages":
			c.RequiresReasoningContentOnAssistantMessages = ptr
		case "tool_result_as_user":
			c.ToolResultAsUser = ptr
		}
	}
	return c
}

// isPrefixID returns true if the ID ends with "-" (matches any model ID
// starting with the prefix).
func isPrefixID(id string) bool {
	return strings.HasSuffix(id, "-") && len(id) > 1
}
