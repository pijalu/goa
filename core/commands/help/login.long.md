<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

/login                          list stored providers + the available sign-on surface
/login:<provider>               start the default (or only) sign-on flow for a provider
/login:<provider>:apikey        store an API key for a provider (any catalog provider)
/login:<provider>:oauth         start OAuth sign-in (copilot/github/openai-codex)
/login:<provider>:oauth:device  headless device-code sign-in
/login:<provider>:<token>       legacy form: the token is stored as an API key

Every provider in the catalog can be given a credential — including gateway
providers that have no dedicated sign-on flow, e.g. /login:vercel:apikey stores
the Vercel AI Gateway key.

Credential resolution order (first hit wins):
  1. provider `api_key` in config
  2. the auth store (~/.goa/tokens.json) — what /login writes
  3. the provider's catalog environment variable, e.g. AI_GATEWAY_API_KEY for
     vercel, OPENAI_API_KEY for openai, ANTHROPIC_API_KEY for anthropic

/login with no arguments lists stored providers, then the curated OAuth
entries, then every catalog provider you can address with :apikey.
