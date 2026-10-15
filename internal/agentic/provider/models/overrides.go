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
// Refresh the snapshot with `go generate ./internal/agentic/provider/models/`
// (runs cmd/genmodels: fetch models.dev, slim, rewrite api.json); the CI build
// regenerates it before compiling so released binaries embed the latest
// catalog.
//
//go:generate go run github.com/pijalu/goa/cmd/genmodels -out api.json
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
// first, then unmapped providers via the fallback. Returns the parsed models in
// catalog order (see runtimeCatalog.order) so the registry's first-wins indexes
// are built identically on every run: a map walk would make the ID-global entry
// for a model listed by several providers — and with it the metadata the
// provider-less fallback reports — depend on map iteration order.
func loadEmbeddedCatalog() []provider.Model {
	cat, err := parseModelsDev(embeddedAPIJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goa: warning: failed to parse embedded api.json: %v\n", err)
		return nil
	}
	var out []provider.Model
	for _, prov := range cat.order {
		out = append(out, cat.byProv[prov]...)
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

// overrideIndex indexes the embedded YAML overrides by provider identity, split
// into the two lookup shapes the registry needs: exact-ID entries (including the
// last-write-wins carve-outs like qwen3.6-plus-free) and prefix entries (family
// pins like qwen3.6- / grok-).
//
// Built once: the YAML is immutable and every lookup happens on the
// model-resolution hot path.
type overrideIndex struct {
	exact  map[provider.Provider]map[string]overrideModel // provider → lower(ID) → entry
	prefix map[provider.Provider][]overrideModel          // provider → prefix entries, YAML order
}

var overrideIdx = buildOverrideIndex()

func buildOverrideIndex() *overrideIndex {
	idx := &overrideIndex{
		exact:  map[provider.Provider]map[string]overrideModel{},
		prefix: map[provider.Provider][]overrideModel{},
	}
	for _, ov := range loadOverrides() {
		prov := provider.Provider(ov.Provider)
		if isPrefixID(ov.ID) {
			idx.prefix[prov] = append(idx.prefix[prov], ov)
			continue
		}
		if idx.exact[prov] == nil {
			idx.exact[prov] = map[string]overrideModel{}
		}
		idx.exact[prov][strings.ToLower(ov.ID)] = ov
	}
	return idx
}

// lookup returns the hand-curated override that decides (prov, id), or nil when
// the YAML says nothing about that pair.
//
// An exact-ID entry always wins over a prefix entry, mirroring the init-time
// order in which exact overrides run after prefix overrides (last write wins).
// Among prefix entries the longest match wins, so a narrower family entry added
// later takes precedence over the broader prefix it extends.
func (idx *overrideIndex) lookup(prov provider.Provider, id string) *overrideModel {
	lower := strings.ToLower(id)
	if byID := idx.exact[prov]; byID != nil {
		if ov, ok := byID[lower]; ok {
			return &ov
		}
	}
	var best *overrideModel
	bestLen := 0
	for _, ov := range idx.prefix[prov] {
		prefix := strings.ToLower(ov.ID)
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			cp := ov
			best = &cp
			bestLen = len(prefix)
		}
	}
	return best
}

// IsCuratedOverride reports whether the given (provider, model ID) pair is
// covered by a hand-curated YAML override — an exact-ID entry, or a prefix
// entry whose declared API/behaviour the model inherits. Catalog (models.dev)
// entries are NOT curated: they carry provider-level defaults that are wrong
// for whole families on multi-surface gateways (the zen 500 class), so the
// wire-format probe may still reroute them. Used to mark Model.ApiSource.
func IsCuratedOverride(prov provider.Provider, id string) bool {
	return overrideIdx.lookup(prov, id) != nil
}

// synthesizeOverrideEntry builds the registry entry for a (provider, model) pair
// the provider's catalog does not list: a model the catalog never carried
// (union-alpha), or one whose membership rotated away upstream — models.dev
// dropped qwen3.6-plus, qwen3.7-max and grok-4.6 from opencode-go while
// opencode kept serving them.
//
// The override decides the entry. The ID-global catalog entry for the same model
// — the copy some *other* provider serves — contributes only model-intrinsic
// metadata (context window, token limits, pricing, reasoning, modalities), never
// its routing: Api and BaseURL are taken from the override alone, because a
// borrowed endpoint points at a different vendor and would silently send the
// request there. Providers whose catalog entry survives are untouched; init
// patches those in place (applyPrefixOverride).
func synthesizeOverrideEntry(prov provider.Provider, id string, ov overrideModel) provider.Model {
	m := provider.Model{ID: id, Provider: prov}
	if borrowed := GetModel(id); borrowed != nil && borrowed.Provider != prov {
		m = *borrowed
		m.Api, m.BaseURL = "", ""
	}
	m = applyOverride(m, ov)
	m.ID, m.Provider = id, prov // the override's ID is the prefix, not the model
	return m
}
