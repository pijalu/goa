// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/auth"
)

func (pm *ProviderManager) BuildStreamOptions() agenticprovider.StreamOptions {
	cfg := pm.cfg.Load()
	pCfg := cfg.GetActiveProviderConfig()
	mCfg, err := cfg.GetActiveModelConfig()
	if err != nil {
		mCfg = config.ModelConfig{}
	}

	defaultRetries := cfg.Execution.Retries
	if defaultRetries <= 0 {
		defaultRetries = 5
	}
	opts := agenticprovider.StreamOptions{MaxRetries: defaultRetries}
	applyProviderStreamOptions(&opts, pCfg, pm.authStore)
	applyModelStreamOptions(&opts, mCfg)
	if opts.Timeout <= 0 {
		// Connection-phase guard (dial → first response header) for providers
		// without a configured timeout; bounds the "stuck in sending" hang
		// without capping long generations on slow local models.
		opts.Timeout = 5 * time.Minute
	}
	if opts.CacheRetention == "" {
		opts.CacheRetention = defaultCacheRetention(pCfg)
	}
	opts.Headers = buildStreamHeaders(pCfg, mCfg)
	return opts
}

// effectiveAPIKey resolves the API key to use for a provider: the explicit
// config key wins, otherwise fall back to the auth store (API key or OAuth).
func (pm *ProviderManager) effectiveAPIKey(provider *config.ProviderConfig) string {
	if provider.APIKey != "" {
		return provider.APIKey
	}
	if pm.authStore == nil {
		return ""
	}
	return resolveAPIKey(pm.authStore, provider.ID)
}

// ResolveAPIKey returns the effective API key for a provider id: the explicit
// config key if present, else the credential from the auth store (API key or
// OAuth access token). Returns "" when no credential is available. Used by the
// plugin bridge so providers authenticated via /login (key in the auth store,
// not in ProviderConfig.APIKey) are still seen as authenticated — otherwise
// the quota plugin treats them as no_api_key and drops them (z.ai #6).
func (pm *ProviderManager) ResolveAPIKey(providerID string) string {
	if pm == nil {
		return ""
	}
	cfg := pm.cfg.Load()
	if cfg == nil {
		return ""
	}
	pCfg := cfg.GetProviderByID(providerID)
	if pCfg == nil {
		return ""
	}
	return pm.effectiveAPIKey(pCfg)
}

func applyProviderStreamOptions(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig, authStore *auth.Store) {
	if pCfg == nil {
		return
	}
	applyProviderAPIKey(opts, pCfg, authStore)
	applyProviderTimeoutRetries(opts, pCfg)
	applyProviderTransportCache(opts, pCfg)
	applyProviderSessionMetadata(opts, pCfg)
}

// applyProviderAPIKey resolves the key from config or the auth store.
func applyProviderAPIKey(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig, authStore *auth.Store) {
	apiKey := pCfg.APIKey
	if apiKey == "" && authStore != nil {
		apiKey = resolveAPIKey(authStore, pCfg.ID)
	}
	if apiKey != "" {
		opts.APIKey = apiKey
	}
	// Codex OAuth: surface the ChatGPT account id so the codex API layer can
	// select the subscription transport (backend-api URL + identity headers).
	applyCodexAccountID(opts, pCfg, authStore)
}

// applyCodexAccountID populates opts.CodexAccountID when the provider's stored
// credential is an OAuth token carrying a ChatGPT account id. An explicit
// config API key or a stored API key means the plain api.openai.com transport,
// so the account id stays empty.
func applyCodexAccountID(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig, authStore *auth.Store) {
	if pCfg == nil || authStore == nil {
		return
	}
	if pCfg.APIKey != "" {
		return // explicit API key wins over OAuth
	}
	storeID := codexStoreID(pCfg.ID)
	if storeID == "" {
		return
	}
	if _, hasKey := authStore.GetAPIKey(storeID); hasKey {
		return // stored API key wins over OAuth
	}
	tokens, ok := authStore.GetOAuth(storeID)
	if !ok || tokens == nil || tokens.AccountID == "" {
		return
	}
	opts.CodexAccountID = tokens.AccountID
}

// codexStoreID maps a configured provider id to the auth-store key that holds
// its Codex credential. /login:openai (and the :codex alias) store under
// "openai"; the catalog "openai-codex" provider shares that credential.
func codexStoreID(providerID string) string {
	switch providerID {
	case "openai", "codex", "openai-codex":
		return "openai"
	default:
		return ""
	}
}

// applyProviderTimeoutRetries applies timeout and retry overrides.
func applyProviderTimeoutRetries(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig) {
	if d := parsePositiveDuration(pCfg.Timeout); d > 0 {
		opts.Timeout = d
	}
	if d := parsePositiveDuration(pCfg.IdleTimeout); d > 0 {
		opts.IdleTimeout = d
	}
	if pCfg.MaxRetries > 0 {
		opts.MaxRetries = pCfg.MaxRetries
	}
	if d := parsePositiveDuration(pCfg.MaxRetryDelay); d > 0 {
		opts.MaxRetryDelay = d
	}
	applyProviderRetryPolicy(opts, pCfg)
}

// applyProviderRetryPolicy resolves the per-provider retry_policy (if any)
// into opts.RetryPolicy. It converts the YAML config into the schema policy,
// fills unset fields from the provider's catalog default (then the package
// default), and ensures the policy's max_retries beats the global
// execution.retries scalar. A nil pCfg.RetryPolicy leaves opts.RetryPolicy as
// nil so the legacy scalar retry behavior (MaxRetries/MaxRetryDelay) applies.
func applyProviderRetryPolicy(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig) {
	if pCfg == nil || pCfg.RetryPolicy == nil {
		return
	}
	configured := &agenticprovider.RetryPolicy{}
	if pCfg.RetryPolicy.Mode == string(agenticprovider.RetryModeAlways) {
		configured.Mode = agenticprovider.RetryModeAlways
	} else if pCfg.RetryPolicy.Mode == string(agenticprovider.RetryModeNormal) {
		configured.Mode = agenticprovider.RetryModeNormal
	}
	configured.MaxRetries = pCfg.RetryPolicy.MaxRetries
	configured.Codes = append([]string(nil), pCfg.RetryPolicy.Codes...)
	b := pCfg.RetryPolicy.Backoff
	if b.InitialMS > 0 {
		configured.Backoff.InitialDelay = time.Duration(b.InitialMS) * time.Millisecond
	}
	if b.MaxMS > 0 {
		configured.Backoff.MaxDelay = time.Duration(b.MaxMS) * time.Millisecond
	}
	configured.Backoff.Jitter = b.Jitter

	catalogDefault := schema.LookupProviderDef(schema.Provider(pCfg.Provider))
	if catalogDefault == nil && pCfg.Provider == "" {
		catalogDefault = schema.MatchProviderByURL(pCfg.Endpoint)
	}
	var catalogPolicy *agenticprovider.RetryPolicy
	if catalogDefault != nil {
		catalogPolicy = catalogDefault.RetryPolicy
	}
	opts.RetryPolicy = schema.ResolveRetryPolicy(configured, catalogPolicy)
}

// applyProviderTransportCache applies transport and cache-retention overrides.
func applyProviderTransportCache(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig) {
	if pCfg.Transport != "" {
		opts.Transport = agenticprovider.Transport(pCfg.Transport)
	}
	if pCfg.CacheRetention != "" {
		opts.CacheRetention = agenticprovider.CacheRetention(pCfg.CacheRetention)
	}
}

// defaultCacheRetention resolves the prompt-cache retention when the user
// config is silent: an explicit user cache_retention already won in
// applyProviderTransportCache, so here the catalog default applies when the
// provider declares one (z.ai opts into long retention so its session cache
// identity — the OpenAI-style prompt_cache_key — reaches the wire), and the
// global default remains short. The catalog is looked up the same way the
// retry-policy fallback does: by provider identity, then by URL when the
// config only carries an endpoint, then by config/wizard ID.
func defaultCacheRetention(pCfg *config.ProviderConfig) agenticprovider.CacheRetention {
	if pCfg == nil {
		return agenticprovider.CacheRetentionShort
	}
	def := schema.LookupProviderDef(schema.Provider(pCfg.Provider))
	if def == nil {
		if pCfg.Provider == "" {
			def = schema.MatchProviderByURL(pCfg.Endpoint)
		}
		if def == nil {
			def = schema.LookupProviderDefByID(pCfg.ID)
		}
	}
	if def != nil && def.DefaultCacheRetention != "" {
		return agenticprovider.CacheRetention(def.DefaultCacheRetention)
	}
	return agenticprovider.CacheRetentionShort
}

// applyProviderSessionMetadata applies session id and metadata overrides.
func applyProviderSessionMetadata(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig) {
	if pCfg.SessionID != "" {
		opts.SessionID = pCfg.SessionID
	}
	if len(pCfg.Metadata) > 0 {
		opts.Metadata = make(map[string]any, len(pCfg.Metadata))
		for k, v := range pCfg.Metadata {
			opts.Metadata[k] = v
		}
	}
}

func applyModelStreamOptions(opts *agenticprovider.StreamOptions, mCfg config.ModelConfig) {
	if mCfg.Temperature != 0 {
		opts.Temperature = &mCfg.Temperature
	}
	if mCfg.MaxTokens > 0 {
		opts.MaxTokens = mCfg.MaxTokens
	}
	// Plumb the configured thinking level onto the wire. Without this the
	// main-agent path never sets StreamOptions.Reasoning, so applyThinking
	// falls back to "medium" — which always-thinking models that accept only
	// low/high/max reject with HTTP 400 (x-preview-f-free). The level passes
	// through raw unless a thinking_level_native_map (Part B) translates it.
	if mCfg.ThinkingLevel != "" {
		opts.Reasoning = agenticprovider.ThinkingLevel(mCfg.ThinkingLevel)
	}
}

func buildStreamHeaders(pCfg *config.ProviderConfig, mCfg config.ModelConfig) map[string]string {
	ua := ""
	if pCfg != nil {
		ua = pCfg.UserAgent
	}
	if ua == "" {
		ua = "goa/" + internal.Version
	}

	headers := make(map[string]string)
	if ua != "" {
		headers["User-Agent"] = ua
	}
	if pCfg != nil {
		for k, v := range pCfg.Headers {
			headers[k] = v
		}
	}
	for k, v := range mCfg.Headers {
		headers[k] = v
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func parsePositiveDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}
