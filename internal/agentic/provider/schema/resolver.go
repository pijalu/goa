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
	profiles []VariantProfile
}

// NewResolver creates a resolver from the given profiles.
func NewResolver(profiles []VariantProfile) *Resolver {
	cp := make([]VariantProfile, len(profiles))
	copy(cp, profiles)
	// Higher specificity first: variant_id/model_id > provider > api.
	sort.SliceStable(cp, func(i, j int) bool {
		return scoreMatch(cp[i].Match) > scoreMatch(cp[j].Match)
	})
	return &Resolver{profiles: cp}
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

// Resolve returns the best matching variant profile for a model.
func (r *Resolver) Resolve(model Model) VariantProfile {
	var best VariantProfile
	bestScore := -1
	for _, p := range r.profiles {
		if !profileMatches(p.Match, model) {
			continue
		}
		s := scoreMatch(p.Match)
		if s > bestScore {
			best = p
			bestScore = s
		}
	}
	return best
}

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
// user-configured profiles.
func ResolveProfile(model Model) VariantProfile {
	profiles, err := LoadAllProfiles()
	if err != nil {
		// Fallback to an empty profile if loading fails.
		return VariantProfile{}
	}
	r := NewResolver(profiles)
	return r.Resolve(model)
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
	mergeThinkingBudgets(d, o)
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
	setIfNonEmpty(&c.MaxTokensField, o.MaxTokensField)
	setIfNonEmpty(&c.ThinkingFormat, o.ThinkingFormat)
	setIfNonEmpty(&c.ReasoningKey, o.ReasoningKey)
	setIfNonEmpty(&c.ImageURLScheme, o.ImageURLScheme)
	mergeMapAny(c.ThinkingExtraBody, o.ThinkingExtraBody)
}

func mergeAuth(a *AuthConfig, o AuthConfig) {
	if o.Method != "" {
		a.Method = o.Method
	}
	setIfNonEmpty(&a.Header, o.Header)
	setIfNonEmpty(&a.Prefix, o.Prefix)
	setIfNonEmpty(&a.TokenPrefix, o.TokenPrefix)
	setIfNonEmpty(&a.BearerPrefix, o.BearerPrefix)
	if len(o.EnvVars) > 0 {
		a.EnvVars = o.EnvVars
	}
	if len(o.OAuthIdentity) > 0 {
		a.OAuthIdentity = o.OAuthIdentity
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
	if o.ToolCallIDRules.MaxLength != 0 {
		t.ToolCallIDRules = o.ToolCallIDRules
	}
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
}

func setIfNonEmpty(dst *string, value string) {
	if value != "" {
		*dst = value
	}
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
