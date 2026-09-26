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
