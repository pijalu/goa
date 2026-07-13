<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Provider Connectivity

This document describes how Goa connects to model providers — intended for
provider operators who want to know what to expect from Goa's HTTP requests.

## User-Agent

All requests carry a `User-Agent` header identifying the client:

| Context | Format | Example |
|---------|--------|---------|
| Provider API calls | `goa/{version} ({os}/{arch})` | `goa/0.1.0-dev (darwin/arm64)` |
| Skill REST API tool | `goa/{version} ({os}/{arch})` | `goa/0.1.0-dev (darwin/arm64)` |
| Custom (via config) | `user_agent` field in provider config | Configurable |

The version component is set at build time via `-ldflags` and defaults to `dev`.

## Authentication

Goa supports multiple authentication methods, configured through provider profiles:

- **Bearer token** (`api_key`): Sent as `Authorization: Bearer {token}`
- **API key header**: A custom header name and value prefix (e.g. `X-API-Key: {key}`)
- **Basic auth**: `Authorization: Basic {base64}`

API keys are read from environment variables referenced in the provider profile
(via `{ENV_VAR}` placeholders), never hardcoded in config files.

## Request format

Goa sends requests using the OpenAI-compatible chat completions API format
by default, with optional support for Anthropic Messages API and Google
Generative AI depending on the configured provider variant.

Key characteristics:

- **Content-Type**: `application/json`
- **Method**: POST to the configured endpoint
- **Streaming**: Server-Sent Events (SSE) for streaming responses; standard JSON
  for non-streaming
- **Timeout**: Configurable per provider (default: none)
- **Retries**: Automatic on 429, 5xx, and network errors up to `max_retries`

## Headers

Custom headers can be added to all requests via the provider config `headers`
field. This is useful for provider-specific requirements (e.g. API version,
organization ID).

## Endpoint resolution

The endpoint is built from the provider config's `endpoint` field. URL templates
with `{ENV_VAR}` placeholders are resolved from the environment at request time.

## Provider detection

Goa auto-detects the provider type from the endpoint and response headers,
supporting OpenAI, Anthropic, Google, llama.cpp, LM Studio, Ollama, and any
OpenAI-compatible endpoint.

## Debugging

Set `GOA_DEBUG_PROVIDER=1` to print the resolved profile and request headers
for each provider call.
