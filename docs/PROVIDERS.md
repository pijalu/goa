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
