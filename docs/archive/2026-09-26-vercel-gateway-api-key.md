# Vercel AI Gateway: no way to add an API key (wrong host + no credential path)

**Date:** 2026-09-26 · **Status:** FIXED — implemented, tested, validated, archived.
**Origin:** bugs.md entry + export `agent-mcp/.goa/exports/goa-export-20260926-113044.zip`.

## Observed

The export's config carried `active_provider: vercel`, `active_model:
stealth/pixel-canary`, a `vercel` provider entry with **`endpoint: ""`**, and a
model entry declaring `provider: vercel` / `model: stealth/pixel-canary`. The
single request in `logs/http.jsonl` went to

```
POST https://api.openai.com/v1/chat/completions   {"model":"stealth/pixel-canary"}
° 400 invalid model ID
```

…i.e. the request was sent to **another vendor's host**, so the user saw a model
error rather than the auth error they reported. Alongside it:

- `/login` offered a hardcoded list (`copilot, github, openai, openai-codex,
  codex, anthropic, kimi`) — vercel and every other catalog provider were absent
  from the list, the completions and `/login` discovery;
- nothing in the add/select-provider flows ever named the catalog's env var
  (`AI_GATEWAY_API_KEY` in `models/api.json`) — the `env` field was dead data;
- key resolution was `ProviderConfig.APIKey` → auth store only, with no
  environment fallback, so a 401 was the only outcome even with the key
  exported.

## Root cause

Three independent gaps:

1. **Routing.** `internal/agentic/provider/runtime.go` `defaultBaseURL` returned
   `https://api.openai.com/v1/chat/completions` for *every* OpenAI-compatible
   model with no base URL, and the catalog carried no base URL for gateway-style
   providers, so an empty `endpoint` could only end up somewhere wrong. The
   manager's resolve paths (`mergeRegistryModel`, `buildFallbackModel`,
   `resolveModelByName`) passed the empty endpoint through.
2. **Auth surface.** The sign-on surface was a hardcoded slice, and the catalog's
   provider-level metadata (id/name/`env`) was never exposed to Go.
3. **Credential chain.** No env-var fallback in either the manager
   (`resolveAPIKey`) or the SDK auth hook, and a missing credential surfaced as a
   bare far-end 401.

## Fix

Single source of truth: the provider catalog.

1. **Catalog gateway entry** — `schema.ProviderDef` for `vercel`
   (`ProviderVercel`, `ApiOpenAICompletions`, `BaseURL
   https://ai-gateway.vercel.sh/v1`, `EnvKeys ["AI_GATEWAY_API_KEY"]`,
   `ModelsDevKey "vercel"`) plus a `variants/vercel.json` profile
   (`auth.required`, env var `AI_GATEWAY_API_KEY`). Because
   `buildModelsDevMappings()` derives from the catalog, the entry also gives the
   models.dev *runtime* catalog a base URL for every vercel model, the wizard/add
   pickers a preset with a real endpoint, and `env_keys.go` the provider's env
   name for `GetEnvAPIKey`.
2. **Runtime routing** — `defaultBaseURL` now resolves an OpenAI-compatible
   model through `openAICompletionsBaseURL`: local → no default; catalog →
   `{catalog base}/chat/completions`; OpenAI identity → the OpenAI host; anything
   else → **no URL**, which fails with the new `schema.NoEndpointError` naming
   the provider instead of silently calling another vendor. The model id is
   never rewritten (`stealth/pixel-canary` reaches the gateway verbatim).
3. **Manager routing** — `providerEndpoint()` (config `endpoint` → config
   `base_url` → catalog base URL) feeds `mergeRegistryModel`,
   `buildFallbackModel` and `resolveModelByName`, and a model's own catalog base
   URL is routed for its API when no provider endpoint exists.
4. **Catalog accessor** — `models.CatalogProviders()` / `models.CatalogEnvKeys()`
   (cached; `Env` added to `ModelsDevProvider` from the models.dev `env` field)
   expose provider id/name/env to the sign-on surface.
5. **Catalog-driven `/login`** — `loginProviderList()` = curated overlay
   (copilot/github/openai/openai-codex/codex/anthropic/kimi with their
   OAuth/device-code kinds) + every catalog provider as an `apikey` entry;
   completions show the env var as the hint (e.g. `vercel → API key:
   AI_GATEWAY_API_KEY`). `loginProviders` is gone.
6. **Credential prompt** — `setupProviderCredential()` resolves config → auth
   store → catalog env var; prompts only when all three are empty, pre-fills from
   the catalog env var (`providerKeyPrefill`), stores via
   `auth.Store.SetAPIKey`, and degrades to an actionable message on a host that
   cannot prompt. Wired into the `/provider` add/select picker, its codex
   API-key branch, and the `/config → add provider` menu.
7. **Env fallback in resolution** — `resolveAPIKey` (manager) and the SDK auth
   hook (`envCandidates`) consult the catalog env names after config/store.
8. **Actionable failure** — `schema.MissingCredentialError` now names the
   provider and the three ways to add a key (`/login:<provider>:apikey`, config
   `api_key`, the catalog env var); `schema.NoEndpointError` does the same for a
   provider that cannot be placed.
9. **Docs** — `docs/PROVIDERS.md` documents the credential chain, the catalog env
   names and `/login:<provider>:apikey`; `/login` help + `docs/COMMANDS.md`
   updated.
10. **Wizard list windowing** — the startup wizard's provider list grew past a
    30-row terminal (its own header scrolled out of view once vercel joined the
    preset list), so `renderProviderType` now windows the list around the
    selection like `renderModelSelect` already did.

### Interpretations recorded

- "Selecting/adding a provider prompts for the key" is implemented as the
  *add/select provide-type* flows (picker, `/config` add, wizard) — switching the
  active provider to an already-configured keyless provider does not interrupt
  with a modal prompt; the actionable request-time error covers it.
- "Skip the prompt when config/env/store already has one" is taken literally:
  an env-var credential *skips* the prompt (the env fallback authenticates the
  request). The env var is therefore the prompt **pre-fill** source
  (`providerKeyPrefill`, asserted directly) rather than a value to confirm.
- The prompt stores in the auth store when one is wired (the production wiring);
  without a vault (scripts, unit tests) the key is handed back to the caller,
  which persists it as the provider's `api_key` exactly as before.

## Tests

New/updated, all green:

| Area | Tests |
|---|---|
| Routing | `TestProviderDefaultEndpoint_VercelGateway`, `TestProviderWithoutEndpoint_FailsActionably`, `TestVercelGateway_RequestRouting`, `TestProviderRequestWithoutCredential_ActionableError` (internal/agentic/provider) |
| Manager routing | `TestResolveActiveModel_VercelGatewayEndpoint`, `TestResolveActiveModel_ConfiguredEndpointStillWins` (provider) |
| `/login` catalog | `TestLoginProviders_IncludeCatalogProviders`, `TestLoginCompletions_CatalogProvider`, `TestLogin_AliasStillResolves` (core/commands) |
| Prompt | `TestSetupProvider_PromptsForKeyWhenMissing`, `TestSetupProvider_SkipsPromptWhenKeyPresent`, `TestSetupProvider_EnvVarPrefill`, `TestSetupProvider_HeadlessIsActionable` (core/commands) |
| Env fallback | `TestResolveAPIKey_EnvFallback` (provider) |
| Fixtures updated for the new contract | `TestGenericRuntimeForAllAPIs` (explicit endpoint per API), `TestPresetProviders_StableOrder` (+`vercel`) |

### RED evidence (recorded before the fix)

```
$ go test -count=1 -run 'TestProviderDefaultEndpoint_VercelGateway|TestProviderWithoutEndpoint_FailsActionably|TestVercelGateway_RequestRouting|TestProviderRequestWithoutCredential_ActionableError' ./internal/agentic/provider/
--- FAIL: TestProviderDefaultEndpoint_VercelGateway
    expected: "https://ai-gateway.vercel.sh/v1/chat/completions"
    actual  : "https://api.openai.com/v1/chat/completions"
--- FAIL: TestProviderWithoutEndpoint_FailsActionably
    An error is expected but got nil.
--- FAIL: TestVercelGateway_RequestRouting
    Expected value not to be nil.      (no request / no Authorization from AI_GATEWAY_API_KEY)
--- FAIL: TestProviderRequestWithoutCredential_ActionableError
    An error is expected but got nil.  (request went out unauthenticated)

$ go test -count=1 -run 'TestLoginProviders_IncludeCatalogProviders|TestLoginCompletions_CatalogProvider|TestLogin_AliasStillResolves' ./core/commands/
--- FAIL: TestLoginProviders_IncludeCatalogProviders
    the /login provider list must offer the catalog provider "vercel"
--- FAIL: TestLoginCompletions_CatalogProvider
    /login:ver* completions = [], want vercel

$ go test -count=1 -run 'TestResolveAPIKey_EnvFallback' ./provider/
--- FAIL: TestResolveAPIKey_EnvFallback/env_used_when_nothing_else_has_a_key
    ResolveAPIKey(vercel) = "", want the catalog env key
```

## Validation (each command run separately)

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `staticcheck ./...` — clean (no findings; the previously noted `probe.go:171`
  S1008 did not reproduce in this run).
- `gocognit -over 15 .` — clean (two findings introduced by the first pass —
  `resolveModelByName` 17, `defaultBaseURL` 15 gocyclo — were fixed by extracting
  `applyResolvedEndpoint`/`lookupProviderModel` and
  `openAICompletionsBaseURL`; no thresholds touched, no `//nolint`).
- `gocyclo -over 12 .` — clean.
- `go test -count=1 -race -cover ./...` — 0 FAIL; `internal/agentic/provider`
  83.0%, `core/commands` 65.0%, `provider` 85.4%.
- Recorded verify command: `go test -count=1 -timeout 240s -run
  'Login|SetupProvider|ResolveAPIKey|Catalog|Credential|Vercel|Endpoint'
  ./core/commands/... ./provider/... ./internal/agentic/provider/...` — PASS.
