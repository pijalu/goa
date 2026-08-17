// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import "time"

// catalog.go — single source of truth for known LLM providers.
//
// Every provider Goa knows about is declared ONCE here as a ProviderDef.
// All other provider-specific behavior is derived from this table:
//   - setup-wizard / config presets        (config/presets.go)
//   - API-key env-var lookup               (internal/agentic/provider/env_keys.go)
//   - URL fingerprinting / compat detect   (internal/agentic/provider/compat_detect.go)
//   - endpoint → identity heuristics       (provider/manager.go)
//   - valid-provider validation            (config/agentic_constants.go)
//   - models.dev catalog mapping           (internal/agentic/provider/models/modelsdev.go)
//   - peak-pricing windows for the TUI peak indicator (tui/footer_render.go)
//
// Adding a provider = adding one ProviderDef entry. No other code changes.
//
// The Compat field carries the wire-level quirks as DATA (not code), so a new
// OpenAI-compatible provider with unusual behavior is a template entry, not a
// fingerprint branch.

// PeakWindow is one recurring daily peak-price window in UTC.
type PeakWindow struct {
	// StartMin and EndMin bound the window as minutes since midnight UTC.
	// EndMin must be greater than StartMin (windows do not wrap past midnight).
	StartMin int
	EndMin   int
	// Weekdays restricts the window to the given UTC weekdays; nil means every day.
	Weekdays []time.Weekday
}

// PeakStatus classifies a time relative to a provider's peak windows.
type PeakStatus int

const (
	// PeakOff is outside every peak window and its grace margin (green).
	PeakOff PeakStatus = iota
	// PeakNear is within peakNearMargin of a window boundary (orange).
	PeakNear
	// PeakOn is inside a peak window (red).
	PeakOn
)

// peakNearMargin is the orange grace margin before/after a peak window, in minutes.
const peakNearMargin = 5

// hhmm converts hours and minutes to minutes since midnight (catalog shorthand).
func hhmm(h, m int) int { return h*60 + m }

// ProviderCompat describes the wire-level behavior of a provider as data.
// The zero value is a fully standard OpenAI-compatible provider.
type ProviderCompat struct {
	// ThinkingFormat selects how thinking is requested/parsed. Empty means the
	// provider's thinking body is derived from its ID (see detectThinkingFormat).
	ThinkingFormat string
	// NonStandard marks providers that don't support the OpenAI "store" field
	// or the newer "developer" role (use "system" instead).
	NonStandard bool
	// UseMaxTokens sends "max_tokens" instead of "max_completion_tokens".
	UseMaxTokens bool
	// NoReasoningEffort disables sending reasoning_effort.
	NoReasoningEffort bool
	// NoCacheRetention disables long cache retention support.
	NoCacheRetention bool
	// NoStrictMode disables OpenAI strict tool-schema mode.
	NoStrictMode bool
	// AnthropicCacheControl uses anthropic-style cache_control breakpoints.
	AnthropicCacheControl bool
	// RequiresReasoningContentOnAssistantMessages keeps reasoning_content on
	// assistant messages (DeepSeek-style multi-turn).
	RequiresReasoningContentOnAssistantMessages bool
	// ToolResultAsUser sends tool results as user messages (local/Gemma/Qwen).
	ToolResultAsUser bool
	// Local marks providers that need no API key (LM Studio, Ollama).
	Local bool
}

// Canonical retry failure codes (dsh llm-retry vocabulary). The retry policy's
// codes[] list names these values; empty codes mean the default transient set
// below. Codes are stable provider-neutral routing keys, never parsed from
// message text by policy consumers.
const (
	// RetryCodeEmptyResponse is a degenerate provider completion that produced
	// no durable content. Repeating it is safe.
	RetryCodeEmptyResponse = "EMPTY_RESPONSE"
	// RetryCodeRateLimit is a provider rate limit (HTTP 429).
	RetryCodeRateLimit = "RATE_LIMIT"
	// RetryCodeServer is a provider server failure (HTTP 5xx).
	RetryCodeServer = "SERVER"
	// RetryCodeTimeout is a request/response timeout (HTTP 408, deadline).
	RetryCodeTimeout = "TIMEOUT"
	// RetryCodeTransport is a network/transport failure (connection reset,
	// refused, EOF, ...).
	RetryCodeTransport = "TRANSPORT"
)

// DefaultRetryCodes are the failure codes eligible for retry in normal mode
// when a policy omits codes (mirrors dsh llm-retry's DEFAULT_RETRYABLE_CODES).
var DefaultRetryCodes = []string{
	RetryCodeEmptyResponse,
	RetryCodeRateLimit,
	RetryCodeServer,
	RetryCodeTimeout,
	RetryCodeTransport,
}

// DefaultRetryPolicy is the package-wide normal-mode retry policy applied when
// neither the provider config nor the catalog entry declares one. It keeps
// Goa's established retry budget (5 retries, 1s→30s exponential, symmetric
// jitter) with the default transient code set.
var DefaultRetryPolicy = &RetryPolicy{
	Mode:       RetryModeNormal,
	MaxRetries: 5,
	Backoff: RetryBackoff{
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		Jitter:       0.25,
	},
	Codes: DefaultRetryCodes,
}

// ResolveRetryPolicy merges an optional configured policy (from provider
// config) with a catalog default into one fully-defaulted policy. The
// configured policy wins per field; catalog defaults fill omissions; the
// package default fills anything still unset. A nil configured policy uses the
// catalog default (or the package default when the catalog has none).
func ResolveRetryPolicy(configured *RetryPolicy, catalogDefault *RetryPolicy) *RetryPolicy {
	out := DefaultRetryPolicy
	if catalogDefault != nil {
		out = catalogDefault
	}
	if configured == nil {
		return cloneRetryPolicy(out)
	}
	merged := *out
	if configured.Mode != "" {
		merged.Mode = configured.Mode
	}
	if configured.MaxRetries != 0 {
		merged.MaxRetries = configured.MaxRetries
	}
	if configured.Backoff.InitialDelay != 0 {
		merged.Backoff.InitialDelay = configured.Backoff.InitialDelay
	}
	if configured.Backoff.MaxDelay != 0 {
		merged.Backoff.MaxDelay = configured.Backoff.MaxDelay
	}
	if configured.Backoff.Jitter != 0 {
		merged.Backoff.Jitter = configured.Backoff.Jitter
	}
	if len(configured.Codes) > 0 {
		merged.Codes = append([]string(nil), configured.Codes...)
	}
	return &merged
}

// cloneRetryPolicy returns a copy of p safe to mutate independently.
func cloneRetryPolicy(p *RetryPolicy) *RetryPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Codes != nil {
		cp.Codes = append([]string(nil), p.Codes...)
	}
	return &cp
}

// ProviderDef is the declarative template for one known provider.
type ProviderDef struct {
	// ID is the config/wizard identifier (e.g. "openrouter", "poolside").
	ID string
	// Name is the human-readable display name (e.g. "OpenRouter").
	Name string
	// Provider is the agentic identity (== ID for most providers).
	Provider Provider
	// API is the wire protocol this provider speaks by default.
	API Api
	// BaseURL is the default endpoint (OpenAI-compatible base URL).
	BaseURL string
	// DefaultModel is the suggested model for the wizard preset.
	DefaultModel string
	// EnvKeys are the API-key environment variables, priority order.
	EnvKeys []string
	// ModelsDevKey is the models.dev catalog key ("" = not on models.dev).
	ModelsDevKey string
	// URLPatterns are lowercase substrings used to fingerprint this provider
	// from a base URL (endpoint heuristics + compat detect). The Provider ID
	// itself is always matched implicitly.
	URLPatterns []string
	// Compat carries the wire-level quirks.
	Compat ProviderCompat
	// Extra holds per-provider request overrides forwarded at stream time.
	Extra map[string]any
	// PeakHours lists the provider's peak-pricing windows in UTC, rendered by
	// the TUI as red inside a window, orange within the grace margin, green
	// otherwise. Empty = no peak indicator (always green).
	PeakHours []PeakWindow
	// RetryPolicy is the provider's default retry policy (nil = the package
	// DefaultRetryPolicy). Resolved per route at provider construction; an
	// explicit provider-config retry_policy overrides it field by field.
	RetryPolicy *RetryPolicy
	// DefaultMaxTokens is the provider's default per-request output-token cap
	// (P21, DS2; dsh llm-deepseek DEFAULT_MAX_TOKENS). It is materialized into
	// the request's max_tokens field when the caller does not set one, so the
	// wire request is always explicit and reconstructable. It is an
	// adapter-configured per-request output cap, NOT a model hard limit (dsh
	// llm README: "defaultMaxTokens is an adapter-configured per-request
	// output cap, not a model hard limit"); zero means no default — the field
	// is omitted and the server applies its own default.
	DefaultMaxTokens int
}

// NeedsAPIKey reports whether this provider requires an API key.
func (d ProviderDef) NeedsAPIKey() bool { return !d.Compat.Local }

// zaiPeakHours is the Z.ai weekday peak window, declared once and shared by
// the "zai" (coding) and "zai-api" (general) catalog entries: Z.ai peak hours
// are Monday to Friday 14:00–18:00 SGT (UTC+8) == 06:00–10:00 UTC Mon–Fri.
var zaiPeakHours = []PeakWindow{
	{
		StartMin: hhmm(6, 0), EndMin: hhmm(10, 0),
		Weekdays: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
	},
}

// PeakStatusAt classifies t relative to the provider's peak windows: PeakOn
// inside a window, PeakNear within peakNearMargin minutes before its start or
// after its end, PeakOff otherwise. Providers without PeakHours are always
// PeakOff (no peak indicator).
func (d ProviderDef) PeakStatusAt(t time.Time) PeakStatus {
	if len(d.PeakHours) == 0 {
		return PeakOff
	}
	t = t.UTC()
	min := t.Hour()*60 + t.Minute()
	wd := t.Weekday()
	for _, w := range d.PeakHours {
		if !w.activeOn(wd) {
			continue
		}
		switch {
		case min >= w.StartMin && min < w.EndMin:
			return PeakOn
		case min >= w.StartMin-peakNearMargin && min < w.StartMin:
			return PeakNear
		case min >= w.EndMin && min < w.EndMin+peakNearMargin:
			return PeakNear
		}
	}
	return PeakOff
}

// activeOn reports whether the window applies on the given UTC weekday.
// A nil Weekdays slice means the window applies every day.
func (w PeakWindow) activeOn(wd time.Weekday) bool {
	if len(w.Weekdays) == 0 {
		return true
	}
	for _, d := range w.Weekdays {
		if d == wd {
			return true
		}
	}
	return false
}

// providerCatalog is the ordered list of known providers. Order matters for
// the setup wizard (preset numbering) and for endpoint-heuristic precedence
// (more-specific URL patterns must precede their substring supersets).
var providerCatalog = []ProviderDef{
	{
		ID: "openai", Name: "OpenAI", Provider: ProviderOpenAI,
		API: ApiOpenAIResponses, BaseURL: "https://api.openai.com/v1",
		DefaultModel: "gpt-4o", EnvKeys: []string{"OPENAI_API_KEY"}, ModelsDevKey: "openai",
		URLPatterns: []string{"api.openai.com"},
	},
	{
		// OpenAI Codex (ChatGPT Plus/Pro subscription). OAuth-first: users
		// authenticate via /login:openai:oauth (browser or device) and the
		// transport targets the chatgpt.com backend API. An OpenAI API key
		// also works (transport falls back to api.openai.com). The credential
		// kind is resolved from the auth store at stream time.
		ID: "openai-codex", Name: "OpenAI Codex", Provider: ProviderOpenAICodex,
		API: ApiOpenAICodexResponses, BaseURL: "https://chatgpt.com/backend-api",
		DefaultModel: "gpt-5.3-codex", EnvKeys: []string{"OPENAI_API_KEY"},
		URLPatterns: []string{"chatgpt.com/backend-api"},
	},
	{
		ID: "lmstudio", Name: "LM Studio", Provider: ProviderLMStudio,
		API: ApiOpenAICompletions, BaseURL: "http://localhost:1234/v1",
		DefaultModel: "local-model",
		URLPatterns:  []string{"localhost:1234", "127.0.0.1:1234"},
		Compat:       ProviderCompat{Local: true, AnthropicCacheControl: true, NonStandard: true, ToolResultAsUser: true},
	},
	{
		ID: "ollama", Name: "Ollama", Provider: ProviderOllama,
		API: ApiOpenAICompletions, BaseURL: "http://localhost:11434/v1",
		DefaultModel: "qwen/qwen3.5-9b",
		URLPatterns:  []string{"localhost:11434", "127.0.0.1:11434"},
		Compat:       ProviderCompat{Local: true, AnthropicCacheControl: true, NonStandard: true, ToolResultAsUser: true},
	},
	{
		ID: "openrouter", Name: "OpenRouter", Provider: ProviderOpenRouter,
		API: ApiOpenAICompletions, BaseURL: "https://openrouter.ai/api/v1",
		DefaultModel: "openrouter/free", EnvKeys: []string{"OPENROUTER_API_KEY"},
		URLPatterns: []string{"openrouter.ai"},
		Compat:      ProviderCompat{ThinkingFormat: "openrouter", AnthropicCacheControl: true},
	},
	{
		ID: "opencode", Name: "OpenCode Zen", Provider: ProviderOpenCode,
		API: ApiOpenAICompletions, BaseURL: "https://opencode.ai/zen/v1",
		DefaultModel: "deepseek-v4-flash", EnvKeys: []string{"OPENCODE_API_KEY"},
		URLPatterns: []string{"opencode.ai"},
		Compat:      ProviderCompat{NonStandard: true},
	},
	{
		ID: "opencode-go", Name: "OpenCode Go", Provider: ProviderOpenCodeGo,
		API: ApiOpenAICompletions, BaseURL: "https://opencode.ai/zen/go/v1",
		DefaultModel: "deepseek-v4-flash", EnvKeys: []string{"OPENCODE_API_KEY"},
		URLPatterns: []string{"opencode.ai/zen/go"},
		Compat:      ProviderCompat{NonStandard: true},
		Extra: map[string]any{
			"reasoning_key":               "reasoning_content",
			"thinking_extra_body":         true,
			"normalize_null_descriptions": true,
			"tool_call_id_max_length":     64,
		},
	},
	{
		ID: "deepseek", Name: "DeepSeek", Provider: ProviderDeepSeek,
		API: ApiOpenAICompletions, BaseURL: "https://api.deepseek.com",
		DefaultModel: "deepseek-v4-flash", EnvKeys: []string{"DEEPSEEK_API_KEY"}, ModelsDevKey: "deepseek",
		URLPatterns: []string{"deepseek.com"},
		// P21 (DS2): adapter-owned output default — dsh llm-deepseek
		// DEFAULT_MAX_TOKENS=256_000 over a DEFAULT_CONTEXT_WINDOW=1_000_000
		// (packages/llm/llm-deepseek/src/adapter.ts:91-93). Materialized into
		// max_tokens by the OpenAI-completions builder when the request omits
		// it, so the wire request is always explicit and reconstructable.
		DefaultMaxTokens: 256000,
		PeakHours: []PeakWindow{
			{StartMin: hhmm(1, 0), EndMin: hhmm(4, 0)},  // 01:00–04:00 UTC daily
			{StartMin: hhmm(6, 0), EndMin: hhmm(10, 0)}, // 06:00–10:00 UTC daily
		},
		Compat: ProviderCompat{
			ThinkingFormat: "deepseek", NonStandard: true,
			RequiresReasoningContentOnAssistantMessages: true,
		},
	},
	{
		ID: "kimi", Name: "Moonshot", Provider: ProviderKimi,
		API: ApiOpenAICompletions, BaseURL: "https://api.moonshot.cn/v1",
		DefaultModel: "kimi-k2.6", EnvKeys: []string{"MOONSHOT_API_KEY", "KIMI_API_KEY"},
		URLPatterns: []string{"api.moonshot.", "moonshotai", "moonshotai-cn"},
		Compat:      ProviderCompat{NonStandard: true, UseMaxTokens: true, NoReasoningEffort: true, NoStrictMode: true},
		Extra: map[string]any{
			"reasoning_key":               "reasoning_content",
			"thinking_extra_body":         true,
			"normalize_null_descriptions": true,
			"tool_call_id_max_length":     64,
		},
	},
	{
		ID: "kimi-code", Name: "Kimi Code", Provider: ProviderKimiCode,
		API: ApiOpenAICompletions, BaseURL: "https://api.kimi.com/coding/v1",
		DefaultModel: "kimi-for-coding", EnvKeys: []string{"KIMI_CODE_API_KEY", "MOONSHOT_API_KEY"},
		URLPatterns: []string{"api.kimi.com/coding"},
		Compat:      ProviderCompat{NonStandard: true, UseMaxTokens: true, NoReasoningEffort: true, NoStrictMode: true},
		Extra: map[string]any{
			"reasoning_key":               "reasoning_content",
			"thinking_extra_body":         true,
			"normalize_null_descriptions": true,
			"tool_call_id_max_length":     64,
		},
	},
	{
		// Z.ai GLM Coding Plan — subscription/quota endpoint. The coding URL is a
		// substring-superset of the general one, so it must precede zai-api.
		ID: "zai", Name: "Z.ai Coding", Provider: ProviderZai,
		API: ApiOpenAICompletions, BaseURL: "https://api.z.ai/api/coding/paas/v4",
		DefaultModel: "glm-5.2", EnvKeys: []string{"ZAI_API_KEY"}, ModelsDevKey: "zai-coding-plan",
		URLPatterns: []string{"api.z.ai/api/coding", "open.bigmodel.cn/api/coding", "zai-coding", "zai-coding-cn", "zai-coding-plan"},
		Compat:      ProviderCompat{ThinkingFormat: "zai", NonStandard: true, NoReasoningEffort: true},
		PeakHours:   zaiPeakHours,
	},
	{
		ID: "zai-api", Name: "Z.ai", Provider: ProviderZaiApi,
		API: ApiOpenAICompletions, BaseURL: "https://api.z.ai/api/paas/v4",
		DefaultModel: "glm-5.2", EnvKeys: []string{"ZAI_API_KEY"}, ModelsDevKey: "zai",
		URLPatterns: []string{"api.z.ai", "open.bigmodel.cn"},
		Compat:      ProviderCompat{ThinkingFormat: "zai", NonStandard: true, NoReasoningEffort: true},
		PeakHours:   zaiPeakHours,
	},
	{
		ID: "poolside", Name: "Poolside", Provider: ProviderPoolside,
		API: ApiOpenAICompletions, BaseURL: "https://inference.poolside.ai/v1",
		DefaultModel: "poolside-default", EnvKeys: []string{"POOLSIDE_API_KEY"},
		URLPatterns: []string{"inference.poolside.ai"},
		Compat:      ProviderCompat{NonStandard: true},
		Extra: map[string]any{
			"reasoning_key":               "reasoning_content",
			"normalize_null_descriptions": true,
		},
	},
	// --- providers known to agentic but without a wizard preset ---
	{
		ID: "anthropic", Name: "Anthropic", Provider: ProviderAnthropic,
		API: ApiAnthropicMessages, BaseURL: "https://api.anthropic.com",
		EnvKeys: []string{"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}, ModelsDevKey: "anthropic",
		URLPatterns: []string{"api.anthropic.com"},
	},
	{
		ID: "google", Name: "Google", Provider: ProviderGoogle,
		API: ApiGoogleGenerativeAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		EnvKeys: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_API_KEY"}, ModelsDevKey: "google",
		URLPatterns: []string{"generativelanguage.googleapis.com"},
	},
	{
		ID: "mistral", Name: "Mistral", Provider: ProviderMistral,
		API: ApiMistralConversations, BaseURL: "https://api.mistral.ai",
		EnvKeys: []string{"MISTRAL_API_KEY"}, ModelsDevKey: "mistral",
		URLPatterns: []string{"api.mistral.ai"},
	},
	{
		ID: "groq", Name: "Groq", Provider: ProviderGroq,
		API: ApiOpenAICompletions, BaseURL: "https://api.groq.com/openai/v1",
		EnvKeys: []string{"GROQ_API_KEY"}, ModelsDevKey: "groq",
		URLPatterns: []string{"api.groq.com"},
	},
	{
		ID: "xai", Name: "xAI", Provider: Provider("xai"),
		API: ApiOpenAICompletions, BaseURL: "https://api.x.ai/v1",
		EnvKeys: []string{"XAI_API_KEY"}, ModelsDevKey: "xai",
		URLPatterns: []string{"api.x.ai"},
		Compat:      ProviderCompat{NonStandard: true, NoReasoningEffort: true},
	},
	{
		ID: "together", Name: "Together", Provider: ProviderTogether,
		API: ApiOpenAICompletions, BaseURL: "https://api.together.xyz/v1",
		EnvKeys:     []string{"TOGETHER_API_KEY"},
		URLPatterns: []string{"api.together.ai", "api.together.xyz"},
		Compat:      ProviderCompat{ThinkingFormat: "together", NonStandard: true, UseMaxTokens: true, NoReasoningEffort: true, NoCacheRetention: true, NoStrictMode: true},
	},
	{
		ID: "fireworks", Name: "Fireworks", Provider: ProviderFireworks,
		API: ApiOpenAICompletions, BaseURL: "https://api.fireworks.ai/inference/v1",
		EnvKeys:     []string{"FIREWORKS_API_KEY"},
		URLPatterns: []string{"fireworks.ai"},
	},
	{
		ID: "perplexity", Name: "Perplexity", Provider: ProviderPerplexity,
		API: ApiOpenAICompletions, BaseURL: "https://api.perplexity.ai",
		EnvKeys:     []string{"PERPLEXITY_API_KEY"},
		URLPatterns: []string{"api.perplexity.ai"},
	},
	{
		ID: "github", Name: "GitHub Copilot", Provider: ProviderGitHub,
		API: ApiOpenAICompletions, BaseURL: "https://api.githubcopilot.com",
		EnvKeys:     []string{"COPILOT_GITHUB_TOKEN", "GITHUB_TOKEN"},
		URLPatterns: []string{"githubcopilot.com"},
	},
	{
		ID: "aws", Name: "AWS Bedrock", Provider: ProviderAWS,
		API: ApiBedrockConverse, EnvKeys: []string{"AWS_ACCESS_KEY_ID"},
	},
	{
		ID: "azure", Name: "Azure OpenAI", Provider: ProviderAzure,
		API: ApiAzureOpenAIResponses, EnvKeys: []string{"AZURE_OPENAI_API_KEY", "AZURE_API_KEY"},
	},
	{
		ID: "custom", Name: "Custom", Provider: ProviderCustom,
		API: ApiOpenAICompletions,
	},
}

// catalogIndex maps provider identity → def for O(1) lookup.
var catalogIndex = buildCatalogIndex()

func buildCatalogIndex() map[Provider]*ProviderDef {
	idx := make(map[Provider]*ProviderDef, len(providerCatalog))
	for i := range providerCatalog {
		d := &providerCatalog[i]
		idx[d.Provider] = d
	}
	return idx
}

// ProviderCatalog returns the ordered list of known provider definitions.
func ProviderCatalog() []ProviderDef { return providerCatalog }

// LookupProviderDef returns the definition for a provider identity, or nil.
func LookupProviderDef(p Provider) *ProviderDef { return catalogIndex[p] }

// LookupProviderDefByID returns the definition for a config/wizard ID, or nil.
func LookupProviderDefByID(id string) *ProviderDef {
	for i := range providerCatalog {
		if providerCatalog[i].ID == id {
			return &providerCatalog[i]
		}
	}
	return nil
}

// MatchProviderByURL returns the catalog entry whose URLPattern best matches
// the given base URL, or nil. The longest matching pattern wins so a
// substring-superset endpoint (z.ai coding ⊃ z.ai general) resolves to the
// more-specific identity regardless of catalog declaration order.
func MatchProviderByURL(baseURL string) *ProviderDef {
	url := lowerASCII(baseURL)
	var best *ProviderDef
	bestLen := -1
	for i := range providerCatalog {
		d := &providerCatalog[i]
		for _, pat := range d.URLPatterns {
			if len(pat) > bestLen && containsSubstr(url, pat) {
				best = d
				bestLen = len(pat)
			}
		}
	}
	return best
}

// MatchProviderByNameOrURL returns the catalog entry matching providerName
// (exact) or baseURL (substring), or nil. A "custom"/empty provider name does
// not short-circuit URL matching — generic identities defer to the URL.
// Mirrors the legacy matchesProviderOrURL semantics (name OR url may match).
func MatchProviderByNameOrURL(providerName Provider, baseURL string) *ProviderDef {
	p := lowerASCII(string(providerName))
	url := lowerASCII(baseURL)
	// Exact provider-name match wins, except for the generic "custom" identity
	// which carries no fingerprint information and must defer to the URL.
	if p != "" && p != string(ProviderCustom) {
		if d := LookupProviderDef(providerName); d != nil {
			return d
		}
	}
	// URL substring match across the catalog (declaration order = precedence).
	if d := MatchProviderByURL(url); d != nil {
		return d
	}
	// Fall back to the generic name match (e.g. custom) if nothing else hit.
	if p != "" {
		return LookupProviderDef(providerName)
	}
	return nil
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func containsSubstr(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
