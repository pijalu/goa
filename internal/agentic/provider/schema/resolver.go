// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Resolver matches models to variant profiles.
type Resolver struct {
	layers []ProfileLayer
}

// NewResolver creates a resolver from a single set of profiles. The profiles
// are treated as one layer: the most specific match wins.
func NewResolver(profiles []VariantProfile) *Resolver {
	return NewLayeredResolver([]ProfileLayer{{Name: layerEmbedded, Profiles: profiles}})
}

// NewLayeredResolver creates a resolver over the config cascade. Layers are in
// increasing precedence order: every layer that matches a model contributes,
// and later layers override earlier ones (docs/PROVIDERS.md "Config cascade").
// Within one layer the most specific match wins.
func NewLayeredResolver(layers []ProfileLayer) *Resolver {
	cp := make([]ProfileLayer, len(layers))
	for i, l := range layers {
		profiles := make([]VariantProfile, len(l.Profiles))
		copy(profiles, l.Profiles)
		// Higher specificity first: variant_id/model_id > provider > api.
		sort.SliceStable(profiles, func(a, b int) bool {
			return scoreMatch(profiles[a].Match) > scoreMatch(profiles[b].Match)
		})
		cp[i] = ProfileLayer{Name: l.Name, Profiles: profiles}
	}
	return &Resolver{layers: cp}
}

func scoreMatch(m ProfileMatch) int {
	score := 0
	if m.API != "" {
		score++
	}
	if m.Provider != "" {
		score += 2
	}
	if m.BaseURL != "" {
		score += 4
	}
	if m.ModelID != "" {
		score += 8
	}
	if m.VariantID != "" {
		score += 16
	}
	return score
}

// Resolve returns the effective variant profile for a model: the cascade
// documented in docs/PROVIDERS.md. Every layer that matches contributes, later
// layers overriding earlier ones, so a user profile that pins one field for a
// model (e.g. compat.thinking_format for one gateway model) refines the
// provider profile instead of replacing it — replacing silently dropped the
// fields it did not restate (max_tokens_field, the reasoning_content echo
// requirement), which is how review finding F5 turned a targeted override into
// a multi-turn failure.
//
// When no layer matches, DefaultProfile() is returned so that unmatched or
// custom providers still get a sane baseline — most importantly standard
// Bearer authentication — instead of an empty profile that silently drops the
// API key (Issue 9: poolside/groq/fireworks/perplexity/zai-api/azure/custom).
func (r *Resolver) Resolve(model Model) VariantProfile {
	var merged VariantProfile
	ids := make([]string, 0, len(r.layers))
	matched := false
	for _, layer := range r.layers {
		best, ok := bestMatch(layer.Profiles, model)
		if !ok {
			continue
		}
		if !matched {
			// First match is taken verbatim: a single-source resolution must
			// be byte-identical to the profile the source declares, not a
			// merge of it onto a zero value (which would silently drop every
			// field the merge helpers do not copy).
			merged = best
			matched = true
		} else {
			merged = MergeProfiles(merged, best)
		}
		if best.ID != "" {
			ids = append(ids, best.ID)
		}
	}
	if !matched {
		return DefaultProfile(model)
	}
	// Name the effective profile after the layers that built it so
	// GOA_DEBUG_PROVIDER=1 shows what was layered, not just the last writer.
	merged.ID = strings.Join(ids, "+")
	return merged
}

// bestMatch returns the most specific profile in one layer that matches the
// model.
func bestMatch(profiles []VariantProfile, model Model) (VariantProfile, bool) {
	var best VariantProfile
	bestScore := -1
	for _, p := range profiles {
		if !profileMatches(p.Match, model) {
			continue
		}
		if s := scoreMatch(p.Match); s > bestScore {
			best, bestScore = p, s
		}
	}
	return best, bestScore >= 0
}

// DefaultProfile returns a sane baseline variant profile for providers that
// have no dedicated variant profile. It models a generic OpenAI-compatible
// endpoint: standard "Authorization: Bearer <key>" auth, max_tokens, OpenAI
// thinking format, usage-in-streaming, OpenAI schema sanitizer, and the common
// retryable-status set. This guarantees any configured or custom provider can
// authenticate and function out of the box; dedicated variant profiles layered
// on top refine behavior for specific providers.
//
// The Match is derived from the model so downstream logging/introspection
// reflects which provider the default was synthesized for.
func DefaultProfile(model Model) VariantProfile {
	return VariantProfile{
		ID: "default-openai-compat",
		Match: ProfileMatch{
			API:      string(model.Api),
			Provider: string(model.Provider),
			BaseURL:  model.BaseURL,
		},
		Defaults: Defaults{
			Temperature: f64(1.0),
			MaxTokens:   intP(4096),
		},
		Compat: CompatFlags{
			SupportsStore:       boolP(false),
			MaxTokensField:      "max_tokens",
			ThinkingFormat:      "openai",
			SupportsPromptCache: false,
			StreamIncludesUsage: true,
		},
		Auth: AuthConfig{
			Method: AuthMethodAPIKey,
			Header: "Authorization",
			Prefix: "Bearer ",
			// Required stays false: local/custom endpoints without a key must
			// still work; when a key IS configured it is now always sent.
			Required: false,
		},
		CachePolicy: CachePolicy{Mode: CacheModeNone, BreakpointCap: 0},
		ToolCompat: ToolCompat{
			ToolCallIDRules: ToolCallIDRules{MaxLength: 40, Alphabet: "[a-zA-Z0-9_-]"},
			SchemaSanitizer: SchemaSanitizerOpenAI,
		},
		ErrorRules: ErrorRules{RetryableStatuses: []int{408, 429, 500, 502, 503, 504}},
	}
}

func f64(v float64) *float64 { return &v }
func intP(v int) *int        { return &v }
func boolP(v bool) *bool     { return &v }

func profileMatches(m ProfileMatch, model Model) bool {
	if m.API != "" && m.API != string(model.Api) {
		return false
	}
	if m.Provider != "" && m.Provider != string(model.Provider) {
		return false
	}
	if m.BaseURL != "" && !strings.Contains(model.BaseURL, m.BaseURL) {
		return false
	}
	if m.ModelID != "" && m.ModelID != model.ID {
		return false
	}
	if m.VariantID != "" && m.VariantID != model.VariantID {
		return false
	}
	return true
}

// ResolveProfile resolves the variant profile for a model using embedded and
// user-configured profiles, layered as the config cascade
// (docs/PROVIDERS.md): embedded defaults, then the user, project and local
// config directories, later sources overriding earlier ones. It then merges the
// model's ThinkingLevelMap into the resolved profile per key: the model map
// (populated from the config's thinking_level_native_map, or from the
// deprecated registry field during the migration) overrides individual profile
// entries while profile keys the model does not mention stay intact. See
// migrateThinkingLevelMap.
func ResolveProfile(model Model) VariantProfile {
	layers, err := LoadProfileLayers()
	if err != nil {
		// Fallback to an empty profile if loading fails.
		return VariantProfile{}
	}
	r := NewLayeredResolver(layers)
	profile := r.Resolve(model)
	profile = migrateThinkingLevelMap(model, profile)
	return profile
}

// migrateThinkingLevelMap merges the model's ThinkingLevelMap into the
// resolved profile per key. Model.ThinkingLevelMap has two sources: the
// config's thinking_level_native_map (a deliberate per-model user override)
// and, during the migration, the deprecated model-registry field. Per-key
// merge means a user remap wins over the profile's own map for the levels it
// lists (e.g. high -> "high" on kimi-code, whose profile maps everything to
// "max") instead of being silently ignored, while unlisted levels keep the
// profile's mapping. Once every variant profile supplies its own
// thinking_level_map, the registry half of this bridge can be removed.
func migrateThinkingLevelMap(model Model, profile VariantProfile) VariantProfile {
	if len(model.ThinkingLevelMap) == 0 {
		return profile
	}
	if profile.Defaults.ThinkingLevelMap == nil {
		profile.Defaults.ThinkingLevelMap = make(ThinkingLevelMap, len(model.ThinkingLevelMap))
	}
	for k, v := range model.ThinkingLevelMap {
		profile.Defaults.ThinkingLevelMap[k] = v
	}
	return profile
}

var urlTemplateRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ResolveURLTemplate replaces {ENV_VAR} placeholders in a URL with environment
// variable values at request time.
func ResolveURLTemplate(url string) string {
	return urlTemplateRe.ReplaceAllStringFunc(url, func(match string) string {
		key := match[1 : len(match)-1]
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		return match
	})
}

// MergeProfiles returns a new profile with override applied on top of base.
func MergeProfiles(base, override VariantProfile) VariantProfile {
	merged := base
	mergeMatch(&merged.Match, override.Match)
	mergeDefaults(&merged.Defaults, override.Defaults)
	mergeCompat(&merged.Compat, override.Compat)
	mergeAuth(&merged.Auth, override.Auth)
	mergeCachePolicy(&merged.CachePolicy, override.CachePolicy)
	mergeToolCompat(&merged.ToolCompat, override.ToolCompat)
	mergeSdkKey(&merged.SdkKey, override.SdkKey)
	mergeErrorRules(&merged.ErrorRules, override.ErrorRules)
	mergeMapString(merged.FieldMappings, override.FieldMappings)
	merged.Headers = append(merged.Headers, override.Headers...)
	if override.ID != "" {
		merged.ID = override.ID
	}
	return merged
}

func mergeMatch(m *ProfileMatch, o ProfileMatch) {
	setIfNonEmpty(&m.API, o.API)
	setIfNonEmpty(&m.Provider, o.Provider)
	setIfNonEmpty(&m.BaseURL, o.BaseURL)
	setIfNonEmpty(&m.ModelID, o.ModelID)
	setIfNonEmpty(&m.VariantID, o.VariantID)
}

func mergeDefaults(d *Defaults, o Defaults) {
	setIfNotNil(&d.Temperature, o.Temperature)
	setIfNotNil(&d.TopP, o.TopP)
	setIfNotNil(&d.TopK, o.TopK)
	setIfNotNil(&d.MaxTokens, o.MaxTokens)
	setIfNonEmpty(&d.Thinking, o.Thinking)
	mergeThinkingLevelMap(d, o)
	mergeThinkingBudgets(d, o)
}

func mergeThinkingLevelMap(d *Defaults, o Defaults) {
	if len(o.ThinkingLevelMap) == 0 {
		return
	}
	if d.ThinkingLevelMap == nil {
		d.ThinkingLevelMap = make(ThinkingLevelMap)
	}
	for k, v := range o.ThinkingLevelMap {
		d.ThinkingLevelMap[k] = v
	}
}

func mergeThinkingBudgets(d *Defaults, o Defaults) {
	if len(o.ThinkingBudgets) == 0 {
		return
	}
	if d.ThinkingBudgets == nil {
		d.ThinkingBudgets = make(ThinkingBudgets)
	}
	for k, v := range o.ThinkingBudgets {
		d.ThinkingBudgets[k] = v
	}
}

func mergeCompat(c *CompatFlags, o CompatFlags) {
	setIfNotNil(&c.SupportsStore, o.SupportsStore)
	setIfNotNil(&c.SupportsEncryptedContent, o.SupportsEncryptedContent)
	setIfNotNil(&c.SupportsTemperature, o.SupportsTemperature)
	setIfNotNil(&c.SupportsToolChoiceNone, o.SupportsToolChoiceNone)
	setIfNonEmpty(&c.MaxTokensField, o.MaxTokensField)
	setIfNonEmpty(&c.ThinkingFormat, o.ThinkingFormat)
	setIfNonEmpty(&c.ReasoningKey, o.ReasoningKey)
	setIfNonEmpty(&c.ImageURLScheme, o.ImageURLScheme)
	mergeMapAny(c.ThinkingExtraBody, o.ThinkingExtraBody)
	// Plain bools carry with OR semantics: they cannot express "explicitly
	// off" in JSON (false == absent), and an override may raise a capability
	// the lower layer did not advertise but must never silently drop one it
	// did — dropping requires_reasoning_content_on_assistant_messages is what
	// turned a targeted user profile into a multi-turn 400 (review finding
	// F5). Fields that genuinely need "explicit off" are tri-state *bool
	// above (supports_store / supports_temperature / supports_tool_choice_none
	// / supports_encrypted_content).
	orBool(&c.RequiresReasoningContentOnAssistantMessages, o.RequiresReasoningContentOnAssistantMessages)
	orBool(&c.ToolStream, o.ToolStream)
	orBool(&c.SystemAsInstructions, o.SystemAsInstructions)
	orBool(&c.SupportsPromptCache, o.SupportsPromptCache)
	orBool(&c.StreamIncludesUsage, o.StreamIncludesUsage)
	orBool(&c.RequiresEmptyToolArguments, o.RequiresEmptyToolArguments)
	orBool(&c.DropNullContent, o.DropNullContent)
	orBool(&c.ImageDetailSupported, o.ImageDetailSupported)
	// RemoteCompaction follows set-if-non-empty semantics: a base/override may
	// raise the capability, but an empty (unspecified) override never clears a
	// capability already advertised by a lower-precedence profile.
	setIfNonEmpty((*string)(&c.RemoteCompaction), string(o.RemoteCompaction))
}

func mergeAuth(a *AuthConfig, o AuthConfig) {
	if o.Method != "" {
		a.Method = o.Method
	}
	setIfNonEmpty(&a.Header, o.Header)
	setIfNonEmpty(&a.Prefix, o.Prefix)
	setIfNonEmpty(&a.TokenPrefix, o.TokenPrefix)
	setIfNonEmpty(&a.BearerPrefix, o.BearerPrefix)
	// Required is a plain bool: JSON cannot distinguish false from absent, so
	// an override can only turn the requirement ON it (or the base's own
	// declaration off — never: see orBool).
	orBool(&a.Required, o.Required)
	if len(o.EnvVars) > 0 {
		a.EnvVars = o.EnvVars
	}
	if len(o.OAuthIdentity) > 0 {
		a.OAuthIdentity = o.OAuthIdentity
	}
	if len(o.PerAPI) > 0 {
		if a.PerAPI == nil {
			a.PerAPI = map[string]APIAuth{}
		}
		for k, v := range o.PerAPI {
			a.PerAPI[k] = v
		}
	}
}

func mergeCachePolicy(c *CachePolicy, o CachePolicy) {
	if o.Mode != "" {
		c.Mode = o.Mode
	}
	setIfNonEmpty(&c.Granularity, o.Granularity)
	if o.Retention != "" {
		c.Retention = o.Retention
	}
	setIfNonEmpty(&c.TTL, o.TTL)
	setIfNonEmpty(&c.AffinityHeader, o.AffinityHeader)
	if o.BreakpointCap != 0 {
		c.BreakpointCap = o.BreakpointCap
	}
}

func mergeToolCompat(t *ToolCompat, o ToolCompat) {
	setIfNonEmpty((*string)(&t.SchemaSanitizer), string(o.SchemaSanitizer))
	setIfNonEmpty(&t.BuiltinFunctionPrefix, o.BuiltinFunctionPrefix)
	setIfNotNil(&t.ToolStreaming, o.ToolStreaming)
	mergeToolCallIDRules(&t.ToolCallIDRules, o.ToolCallIDRules)
	// Plain bools: OR semantics, same reasoning as mergeCompat.
	orBool(&t.ToolResultAsUser, o.ToolResultAsUser)
	orBool(&t.RequiresToolResultName, o.RequiresToolResultName)
	orBool(&t.RequiresAssistantAfterToolResult, o.RequiresAssistantAfterToolResult)
	orBool(&t.NormalizeNullDescriptions, o.NormalizeNullDescriptions)
	orBool(&t.SupportsParallelToolCalls, o.SupportsParallelToolCalls)
}

// mergeToolCallIDRules merges the ID-normalization rules field by field so an
// override that names one part (say the alphabet) keeps the base's other parts.
func mergeToolCallIDRules(t *ToolCallIDRules, o ToolCallIDRules) {
	if o.MaxLength != 0 {
		t.MaxLength = o.MaxLength
	}
	setIfNonEmpty(&t.Alphabet, o.Alphabet)
	setIfNonEmpty(&t.Prefix, o.Prefix)
	setIfNonEmpty(&t.Separator, o.Separator)
	setIfNonEmpty(&t.ForeignPrefix, o.ForeignPrefix)
	orBool(&t.HashBased, o.HashBased)
}

func mergeSdkKey(s *SdkKeyConfig, o SdkKeyConfig) {
	if o.Namespace != "" {
		s.Namespace = o.Namespace
	}
	mergeMapString(s.Mappings, o.Mappings)
}

func mergeErrorRules(e *ErrorRules, o ErrorRules) {
	setIfNonEmpty(&e.RetryAfterHeader, o.RetryAfterHeader)
	setIfNonEmpty(&e.RetryAfterMsHeader, o.RetryAfterMsHeader)
	if len(o.RetryableStatuses) > 0 {
		e.RetryableStatuses = o.RetryableStatuses
	}
	if len(o.RetryOnceStatuses) > 0 {
		e.RetryOnceStatuses = o.RetryOnceStatuses
	}
	if len(o.ContextOverflowPatterns) > 0 {
		e.ContextOverflowPatterns = o.ContextOverflowPatterns
	}
	if len(o.NonOverflowPatterns) > 0 {
		e.NonOverflowPatterns = o.NonOverflowPatterns
	}
}

func setIfNonEmpty(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}

// orBool raises dst when the override sets the plain bool. A plain bool cannot
// distinguish "explicitly false" from "absent" in JSON, so a merge can only
// ever switch a capability on — never off. That is why fields with a real
// need for "explicitly off" are tri-state *bool in the schema.
func orBool(dst *bool, value bool) {
	*dst = *dst || value
}

func setIfNotNil[T any](dst **T, value *T) {
	if value != nil {
		*dst = value
	}
}

func mergeMapString(dst map[string]string, src map[string]string) {
	if len(src) == 0 {
		return
	}
	if dst == nil {
		return
	}
	for k, v := range src {
		dst[k] = v
	}
}

func mergeMapAny(dst map[string]any, src map[string]any) {
	if len(src) == 0 {
		return
	}
	if dst == nil {
		return
	}
	for k, v := range src {
		dst[k] = v
	}
}

// LoadEmbeddedProfile loads a single embedded profile by id.
func LoadEmbeddedProfile(id string) (VariantProfile, error) {
	profiles, err := LoadEmbeddedProfiles()
	if err != nil {
		return VariantProfile{}, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return VariantProfile{}, fmt.Errorf("embedded profile %q not found", id)
}
