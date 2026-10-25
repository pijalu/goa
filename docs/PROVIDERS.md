<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa Provider Configuration

Goa uses a config-driven provider architecture. Each provider variant is
described by a JSON profile that lives in
`internal/agentic/provider/schema/variants/` (embedded defaults) or can be
overridden by user configuration.

## Config cascade

Variant profiles are merged in the following order, with later sources
overriding earlier ones:

1. Embedded defaults shipped with Goa (`variants/*.json`).
2. User configuration in `~/.goa/providers/*.json`.
3. Project configuration in `./.goa/providers/*.json`.
4. Local overrides in `./.goa/providers.local/*.json`.
5. Environment variables referenced by URL templates (`{ENV_VAR}`).
6. The `Model.VariantID` field.

The merge is literal: every source that matches a model contributes its fields,
and a later source overrides only the fields it sets. A user profile that pins
one field for one model (say `compat.thinking_format` for a single gateway
model) therefore *refines* the provider profile instead of replacing it —
which is what `max_tokens_field`, the auth block, and things like
`requires_reasoning_content_on_assistant_messages` depend on. Two caveats:

- **Within one source, only the most specific match applies.** `model_id`
  (score 8) beats `base_url` (4) beats `provider` (2) beats `api` (1), so a
  broad profile in the same directory is not layered under a narrow one.
- **Plain booleans carry with OR semantics.** JSON cannot distinguish
  `false` from absent, so a merge can only switch a capability on, never off.
  Fields that genuinely need "explicitly off" are tri-state (`*bool`) in the
  schema: `supports_store`, `supports_temperature`, `supports_tool_choice_none`,
  `supports_encrypted_content`, `tool_streaming`.

The resolved profile is named after the sources that built it
(`opencode-go+catjam-effort-only`), which is what `GOA_DEBUG_PROVIDER=1`
prints.

## Profile schema

A minimal profile looks like this:

```json
{
  "id": "my-provider",
  "match": {
    "api": "openai-completions",
    "provider": "custom",
    "base_url": "http://localhost:1234"
  },
  "defaults": {
    "temperature": 0.7,
    "max_tokens": 4096
  },
  "compat": {
    "supports_store": false,
    "max_tokens_field": "max_tokens",
    "thinking_format": "none"
  },
  "auth": {
    "method": "api_key",
    "env_vars": ["MY_API_KEY"],
    "header": "Authorization",
    "prefix": "Bearer "
  },
  "cache_policy": {
    "mode": "none",
    "breakpoint_cap": 0
  },
  "tool_compat": {
    "tool_call_id_rules": {
      "max_length": 40,
      "alphabet": "[a-zA-Z0-9_-]"
    },
    "schema_sanitizer": "openai"
  },
  "error_rules": {
    "retryable_statuses": [429, 500, 502, 503, 504]
  }
}
```

## Request-side thinking body (`compat.thinking_format`)

`compat.thinking_format` selects the field(s) a request uses to control
reasoning. It is a *request* vocabulary, not the response-side
`Model.ThinkingFormat` (which names the field the reasoning text streams back
in):

| value | request fields | used by |
|---|---|---|
| `openai` (default for OpenAI-compatible surfaces) | `reasoning_effort` | opencode / opencode-go (zen), kimi-code, vercel, generic OpenAI-compatible endpoints |
| `deepseek` | `thinking: {type: enabled}` + `reasoning_effort` | DeepSeek's own API, string-thinking endpoints |
| `zai` | `thinking: {type: enabled, clear_thinking: false}` | z.ai GLM |
| `together` | `reasoning: {enabled: true}` + `reasoning_effort` | Together |
| `openrouter` | `reasoning: {effort: …}` | OpenRouter |
| `qwen*` | `thinking: true` | Qwen chat-template endpoints |
| `none` / unset | nothing is sent | endpoints that reject every reasoning parameter |

Rules worth knowing before editing a profile:

- **Never send a field an upstream does not know.** Proxying gateways forward
  unknown arguments to the upstream instead of dropping them, and strict
  upstreams reject them: the OpenCode Zen *Go* gateway answered
  `thinking: {type: …}` with `HTTP 400 [unsupported_parameter] Unrecognized
  request argument supplied: thinking.` (exports 20261013-220109,
  20261013-222008) even though the model reasoned fine without it.
- **Match the reference client.** The zen surfaces are declared on models.dev
  with npm `@ai-sdk/openai-compatible`, which the OpenCode client maps to its
  `openaiChat` protocol — `reasoning_effort` only (see `packages/core/src/`
  `variant.ts` upstream). That is why `opencode` / `opencode-go` use `openai`
  here.
- **Level values are per model.** models.dev publishes the accepted
  `reasoning_options` values per model; where a model accepts a restricted set,
  pin it with a `thinking_level_map` in `model_overrides.yaml` (see MiMo) or
  per model with `thinking_level_native_map` in `config.yaml`.
- **The object form cannot be half-sent.** A `thinking` object is either
  accepted by an upstream or fatal; there is no "disabled" spelling that a
  strict upstream tolerates (`thinking: {type: "disabled"}` 400s exactly like
  the enabled one on the same upstreams).
- **A model that cannot reason gets no reasoning field at all.** The request
  carries a reasoning field only when the model is reasoning-capable, the
  caller set an explicit level (`thinking_level`, which covers a catalog entry
  that lags the model), or the profile declares an always-on mode
  (`defaults.thinking`, e.g. kimi-code/moonshot).

### Per-model reasoning metadata from the catalog

models.dev states more than the reasoning flag, and Goa imports it whenever the
registry entry has no curated override:

- `reasoning_options: [{type: effort, values: [...]}]` becomes a thinking-level
  map with identity entries, one per canonical level the model declares. The
  effect is clamping rather than translation: a configured level the model does
  not list resolves to the nearest one it does, instead of reaching an endpoint
  that answers `400 reasoning_effort must be low, high, xhigh, max`. `toggle`
  (on/off, no levels) and `budget_tokens` (the separate `thinking_budgets`
  domain) are deliberately ignored.
- `interleaved: {field: reasoning_content}` becomes `Model.ThinkingFormat`, the
  field reasoning is replayed in on assistant messages. Only names Goa has
  semantics for are mapped: `reasoning_details` (OpenRouter-style) has no
  replay path here and is deliberately left unset rather than guessed.
- Curated `model_overrides.yaml` entries are applied on top and replace both,
  so hand-verified provider semantics always win over catalog data.

### Rejected parameters are dropped and retried once

When an endpoint answers `400`/`422` naming a request field as an unsupported
parameter (`{"error":{"param":"thinking","message":"…[unsupported_parameter]
Unrecognized request argument supplied: thinking."}}`, or the same complaint in
free text), the agent drops that one field from the request and retries instead
of failing the turn — the field is Goa's to remove, and a strict upstream is not
a permanent outage. The behavior is bounded: one field per attempt, never the
same field twice, at most three fields, and a suppressed field is not carried
into later requests of the session. The user sees it as a progress line
(`Provider rejected request parameter "thinking" — retrying without it.`) and a
warning in the agent log.

## Provider credentials: API keys, env vars and `/login`

Every provider Goa can select can also be given a credential. The credential
chain is resolved in this order, first hit wins:

1. `api_key` on the provider in `config.yaml` (the explicit override).
2. The auth store `~/.goa/tokens.json` — what `/login` writes.
3. The provider's **catalog environment variable** (models.dev `env` names).

### Storing a key

```
/login:<provider>:apikey
```

works for **any** catalog provider, curated or not:

```
/login:vercel:apikey        # Vercel AI Gateway
/login:openrouter:apikey    # OpenRouter
/login:tensorx:apikey       # any other catalog provider
```

`/login` with no arguments lists the stored credentials, the curated OAuth
entries (copilot/github/openai-codex/anthropic/kimi — OAuth and device-code),
and every remaining catalog provider addressable with `:apikey`. The argument
completions show the env var each provider reads.

### Catalog environment variables

These are the variables the catalog declares for common providers. `goa` reads
them when neither config nor the auth store holds a key — which is how
headless/CI runs authenticate:

| Provider | Env var(s) |
|---|---|
| vercel (Vercel AI Gateway) | `AI_GATEWAY_API_KEY` |
| openai | `OPENAI_API_KEY` |
| openai-codex | `OPENAI_API_KEY` |
| anthropic | `ANTHROPIC_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` |
| openrouter | `OPENROUTER_API_KEY` |
| deepseek | `DEEPSEEK_API_KEY` |
| kimi (Moonshot) | `MOONSHOT_API_KEY`, `KIMI_API_KEY` |
| kimi-code | `KIMI_CODE_API_KEY`, `MOONSHOT_API_KEY` |
| zai / zai-api | `ZAI_API_KEY` |
| google | `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `GOOGLE_GENAI_API_KEY` |
| xai | `XAI_API_KEY` |
| groq | `GROQ_API_KEY` |
| together | `TOGETHER_API_KEY` |
| fireworks | `FIREWORKS_API_KEY` |
| perplexity | `PERPLEXITY_API_KEY` |
| github (Copilot) | `COPILOT_GITHUB_TOKEN`, `GITHUB_TOKEN` |
| opencode / opencode-go | `OPENCODE_API_KEY` |
| poolside | `POOLSIDE_API_KEY` |
| mistral | `MISTRAL_API_KEY` |

Providers without a curated entry fall back to `{PROVIDER_UPPER_SNAKE}_API_KEY`
(e.g. `TENSORX_API_KEY`); the full list is whatever `models/api.json` declares,
surfaced by `/login` and by the argument completions.

### Z.ai / BigModel Start Plan (free GLM tier)

The Start Plan providers (`zai-start-plan`, `bigmodel-start-plan`) are the free
GLM tier served by the zcode plan backend. They ship three models
(GLM-5.3-Flash, GLM-5.2, GLM-5-Turbo) and are quota-based: usage draws down a
bundled allowance instead of a bill.

- **Sign in**: `/login:zai:oauth` or `/login:bigmodel:oauth` (aliases
  `z.ai-start-plan` / `bigmodel-start-plan`). This stores the zcode JWT the
  runtime and the quota surface both use. A config `api_key` or the
  `ZAI_START_PLAN_API_KEY` / `BIGMODEL_START_PLAN_API_KEY` environment
  variables also authenticate inference.
- **Select**: `/provider zai-start-plan` (or set the provider in config.yaml).
- **Quota**: `/quota` shows a Start Plan row per family with the per-model
  buckets; the status-bar segment shows the active family's usage.
- **Device identity**: goa mints one `deviceMid` per install (shared by the
  runtime and plugins) and sends it as `X-Device-Mid` — the plan backend
  rejects balance and inference calls without it.
- **Endpoint overrides**: `GOA_ZCODE_API_URL` (default
  `https://zcode.z.ai/api/v1`) and `GOA_ZCODE_APP_VERSION` (default `0.0.0`).

### When no credential exists

A request that cannot be authenticated fails **before** it is sent, naming the
provider and the three ways to add a key:

```
no API key found for provider "vercel": checked options.api_key, AI_GATEWAY_API_KEY
 — add one with /login:vercel:apikey, set api_key on the provider in config,
 or provide it in the environment (AI_GATEWAY_API_KEY)
```

## Catalog gateways and default endpoints

Some catalog providers are gateways: their models.dev entry carries only
`npm`/`env`, no base URL, and they address models with a vendor-namespaced id
(e.g. `stealth/pixel-canary` on the Vercel AI Gateway). For those, the base URL
comes from the provider catalog:

| Provider | Default base URL |
|---|---|
| vercel | `https://ai-gateway.vercel.sh/v1` |

A model resolved for such a provider gets `{base}/chat/completions` with the id
passed through unchanged — Goa never rewrites `stealth/pixel-canary`. An empty
`endpoint` no longer falls back to `api.openai.com`: a non-OpenAI provider with
no endpoint (and no catalog base URL) fails with an actionable
`provider "X" has no endpoint` error instead of silently calling another
vendor's API.

## Adding a custom provider without code changes

1. Create a profile file in `~/.goa/providers/my-provider.json` matching the
   schema above.
2. Reference it in your model definition using `BaseURL` or `VariantID`.
3. The generic runtime resolves the profile and builds the request
   automatically.

## URL templates

Any `{ENV_VAR}` placeholder in `BaseURL` is resolved at request time from the
environment. This is useful for API keys in query strings or dynamic endpoints.

## Expression support

Profiles may contain `field_mappings` entries using a small template syntax:
variables are referenced with `$name` or dot-paths, and environment variables
use `{ENV_VAR}`.

## Debug tools

- `go test ./internal/agentic/provider/...` runs the full provider test suite.
- Set `GOA_DEBUG_PROVIDER=1` to print the resolved profile for each request.
